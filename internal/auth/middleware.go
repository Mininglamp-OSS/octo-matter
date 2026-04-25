package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// UserAuthMiddleware is a stub middleware that parses the token header
// and sets uid, name, and role in the gin context.
// In production, this will call dmworkim's /internal/v1/auth/verify endpoint.
func UserAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("token")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"msg": "missing token header"})
			return
		}

		// Stub: parse token as "uid@name@role" format (matching dmworkim's Redis format)
		parts := strings.SplitN(token, "@", 3)
		if len(parts) != 3 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"msg": "invalid token format"})
			return
		}

		c.Set("uid", parts[0])
		c.Set("name", parts[1])
		c.Set("role", parts[2])
		c.Next()
	}
}

// SpaceMiddleware is a stub middleware that reads X-Space-ID header or space_id query parameter
// and sets space_id in the gin context.
// In production, this will call dmworkim's /internal/v1/auth/space-check endpoint.
func SpaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		spaceID := c.GetHeader("X-Space-ID")
		if spaceID == "" {
			spaceID = c.Query("space_id")
		}
		if spaceID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"msg": "missing space_id"})
			return
		}

		c.Set("space_id", spaceID)
		c.Next()
	}
}
