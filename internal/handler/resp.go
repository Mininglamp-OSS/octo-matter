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

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validUUID(id string) bool {
	return uuidRegex.MatchString(id)
}

func ok(c *gin.Context, data any) {
	if data == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, data)
}

func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, data)
}

func paginated(c *gin.Context, data any, hasMore bool, nextCursor string) {
	pg := gin.H{"has_more": hasMore}
	if hasMore && nextCursor != "" {
		pg["next_cursor"] = nextCursor
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "pagination": pg})
}

func failCode(c *gin.Context, status int, code, msg string, details map[string]any) {
	body := gin.H{"code": code, "message": msg}
	if len(details) > 0 {
		body["details"] = details
	}
	c.AbortWithStatusJSON(status, gin.H{"error": body})
}

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
		return c.GetString("uid")
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
