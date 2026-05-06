package handler

import (
	"errors"
	"log"
	"net/http"
	"regexp"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

// uuidRegex validates UUID v4 format on path parameters to avoid unnecessary
// DB round-trips for garbage input.
var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validUUID(id string) bool {
	return uuidRegex.MatchString(id)
}

// This file implements the REST response envelope defined in DESIGN.md:
//
//   Success: bare resource object (no {code,msg,data} wrapper). HTTP status
//            (200/201/204) carries success.
//   Error:   {"error":{"code":"<ENUM>","message":"<text>","details":{...}}}
//
// `code` is a stable machine-readable enum from apperr. Clients branch on
// `code`, never on `message` (which is human-readable and may change).

// ok writes a 200 response with the value as the body. Pass nil for 204.
func ok(c *gin.Context, data any) {
	if data == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, data)
}

// created writes a 201 with the created resource as the body.
func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, data)
}

// paginated wraps a list result in the envelope defined in DESIGN.md:
//
//	{"data":[...],"pagination":{"has_more":bool,"next_cursor":"..."}}
//
// `nextCursor` is only emitted when hasMore is true.
func paginated(c *gin.Context, data any, hasMore bool, nextCursor string) {
	pg := gin.H{"has_more": hasMore}
	if hasMore && nextCursor != "" {
		pg["next_cursor"] = nextCursor
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "pagination": pg})
}

// failCode writes the REST error envelope with an explicit business code. Use
// this directly at handler-layer validation points (malformed JSON, missing
// params) where the service layer wasn't involved.
func failCode(c *gin.Context, status int, code, msg string, details map[string]any) {
	body := gin.H{"code": code, "message": msg}
	if len(details) > 0 {
		body["details"] = details
	}
	c.AbortWithStatusJSON(status, gin.H{"error": body})
}

// respondErr translates service/repo errors to the REST error envelope.
//   - *apperr.AppError renders with its own code/message/details/status.
//   - Bare sentinels (ErrNotFound/ErrForbidden/ErrInvalidInput) fall back to a
//     generic code so we never leak the wrapped Go error string to clients.
//   - repository.ErrInvalidCursor is a client-fixable 400.
//   - Everything else is a 500 INTERNAL_ERROR; the full error is logged for
//     operator correlation via the gin request log.
func respondErr(c *gin.Context, err error) {
	if ae, ok := apperr.AsAppError(err); ok {
		failCode(c, ae.HTTPStatus(), ae.Code(), ae.Message(), ae.Details())
		return
	}
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		failCode(c, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, apperr.ErrForbidden):
		failCode(c, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
	case errors.Is(err, apperr.ErrInvalidInput):
		// InvalidInput wraps with the format "invalid input: <msg>" — surface
		// just the actionable message to the client.
		msg := err.Error()
		const prefix = "invalid input: "
		if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
			msg = msg[len(prefix):]
		}
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", msg, nil)
	case errors.Is(err, repository.ErrInvalidCursor):
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid cursor", nil)
	default:
		log.Printf("internal error: %v", err)
		failCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error", nil)
	}
}

// bindJSONErr renders a JSON binding failure as 400 VALIDATION_ERROR.
// Detects body-too-large to return 413 instead of generic 400.
func bindJSONErr(c *gin.Context, err error) {
	if err.Error() == "http: request body too large" {
		failCode(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds size limit", nil)
		return
	}
	failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
}

func uid(c *gin.Context) string {
	return c.GetString("uid")
}

func userName(c *gin.Context) string {
	name := c.GetString("name")
	if name == "" {
		return c.GetString("uid") // fallback to uid if name not set
	}
	return name
}

func spaceID(c *gin.Context) string {
	return c.GetString("space_id")
}

func relatedUIDs(c *gin.Context) []string {
	if v, exists := c.Get("related_uids"); exists {
		if uids, ok := v.([]string); ok && len(uids) > 0 {
			return uids
		}
	}
	return []string{uid(c)}
}
