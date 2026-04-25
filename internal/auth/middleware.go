package auth

import (
	"log"
	"net/http"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/config"
	"github.com/gin-gonic/gin"
)

// NewUserAuthMiddleware returns the correct auth middleware for the configured
// AuthMode. In stub mode it logs a loud startup WARN so the operator can see
// the service is accepting unsigned "uid@name@role" tokens. Remote mode is not
// yet implemented and panics at construction time — callers should already
// have failed fast in Config.Validate, this is a defence-in-depth.
func NewUserAuthMiddleware(mode config.AuthMode) gin.HandlerFunc {
	switch mode {
	case config.AuthModeStub:
		log.Printf("WARN: auth running in STUB mode — 'token: uid@name@role' headers are accepted without verification. DO NOT use in production.")
		return stubUserAuth()
	case config.AuthModeRemote:
		panic("auth: remote mode selected but not implemented — refusing to start to avoid silently falling back to stub")
	default:
		panic("auth: unknown AuthMode " + string(mode))
	}
}

// stubUserAuth parses the "token" header as "uid@name@role". It performs no
// cryptographic verification; any caller can impersonate any uid. Only use in
// development or against trusted networks.
func stubUserAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("token")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"msg": "missing token header"})
			return
		}

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

// SpaceMiddleware reads the X-Space-ID header and stores it in the gin context.
// The query-string fallback has been removed: query strings leak into access
// logs and referrer headers, so space IDs must travel via a request header only.
func SpaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		spaceID := c.GetHeader("X-Space-ID")
		if spaceID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"msg": "missing X-Space-ID header"})
			return
		}
		c.Set("space_id", spaceID)
		c.Next()
	}
}
