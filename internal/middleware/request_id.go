package middleware

import (
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var validRequestID = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

// RequestID injects X-Request-ID into context and response header.
// If the client sends a valid one (alphanumeric, max 128 chars), it is reused;
// otherwise a new UUID is generated.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" || !validRequestID.MatchString(id) {
			id = uuid.New().String()
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
