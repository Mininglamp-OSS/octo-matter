package handler

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": data})
}

func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"code": 0, "msg": "ok", "data": data})
}

func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    http.StatusText(status),
			"message": msg,
		},
	})
}

// respondErr maps service/repo errors to HTTP responses without leaking internals.
// Typed errors (apperr.*) map to 404/403/400 with their short sentinel message.
// ErrInvalidCursor from the repo layer is treated as a client-fixable 400. All
// other errors map to 500 with a generic message; the full error is logged so
// operators can correlate via the gin request log.
func respondErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		fail(c, http.StatusNotFound, "not found")
	case errors.Is(err, apperr.ErrForbidden):
		fail(c, http.StatusForbidden, "forbidden")
	case errors.Is(err, apperr.ErrInvalidInput):
		// InvalidInput wraps with the format "invalid input: <msg>" — strip the
		// prefix so the client sees only the actionable message.
		msg := strings.TrimPrefix(err.Error(), "invalid input: ")
		fail(c, http.StatusBadRequest, msg)
	case errors.Is(err, repository.ErrInvalidCursor):
		fail(c, http.StatusBadRequest, "invalid cursor")
	default:
		log.Printf("internal error: %v", err)
		fail(c, http.StatusInternalServerError, "internal error")
	}
}

func uid(c *gin.Context) string {
	return c.GetString("uid")
}

func spaceID(c *gin.Context) string {
	return c.GetString("space_id")
}
