package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

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
func bindJSONErr(c *gin.Context, err error) {
	failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
}

func uid(c *gin.Context) string {
	return c.GetString("uid")
}

func spaceID(c *gin.Context) string {
	return c.GetString("space_id")
}
