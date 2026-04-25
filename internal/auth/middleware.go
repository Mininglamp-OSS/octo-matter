package auth

import (
	"log"
	"net/http"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/config"
	"github.com/gin-gonic/gin"
)

// writeAuthErr renders the REST error envelope directly (handler package can't
// be imported here — it depends on us). Keep the shape identical to
// handler.respondErr so clients see one format everywhere.
func writeAuthErr(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

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
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing token header")
			return
		}

		parts := strings.SplitN(token, "@", 3)
		if len(parts) != 3 {
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid token format")
			return
		}

		c.Set("uid", parts[0])
		c.Set("name", parts[1])
		c.Set("role", parts[2])
		c.Next()
	}
}

// SpaceMiddleware reads the X-Space-ID header and stores it in the gin context.
// Header-only: query strings leak into access logs and Referer headers, so
// space IDs must travel via a request header exclusively (DESIGN.md v5).
func SpaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		spaceID := c.GetHeader("X-Space-ID")
		if spaceID == "" {
			writeAuthErr(c, http.StatusBadRequest, "VALIDATION_ERROR", "missing X-Space-ID header")
			return
		}
		c.Set("space_id", spaceID)
		c.Next()
	}
}
