package auth

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Mininglamp-OSS/octo-auth/sdk-go/client"
	"github.com/Mininglamp-OSS/octo-matter/internal/config"
)

// NewAuthMiddleware creates the auth middleware using octo-auth SDK.
// Auto-routes based on header: "token" → user verify, "Authorization: Bot" → bot verify.
// Results are cached locally (60s TTL) to avoid network round-trips.
func NewAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	c := client.New(client.Config{
		AuthURL:     cfg.AuthURL,
		InternalKey: cfg.InternalKey,
		CacheTTL:    60 * time.Second,
	})
	return c.AuthMiddleware()
}

// SpaceMiddleware reads the X-Space-ID header and stores it in the gin context.
// Bot auth sets space_id internally; this middleware fills it for user requests.
func SpaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, exists := c.Get("space_id"); exists {
			c.Next()
			return
		}
		spaceID := c.GetHeader("X-Space-ID")
		if spaceID == "" {
			c.AbortWithStatusJSON(400, gin.H{
				"error": gin.H{"code": "VALIDATION_ERROR", "message": "missing X-Space-ID header"},
			})
			return
		}
		c.Set("space_id", spaceID)
		c.Next()
	}
}
