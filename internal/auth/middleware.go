package auth

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Mininglamp-OSS/octo-auth/sdk-go/middleware"
	"github.com/Mininglamp-OSS/octo-matter/internal/config"
)

// writeAuthErr renders the REST error envelope directly (handler package can't
// be imported here — it depends on us). Keep the shape identical to
// handler.respondErr so clients see one format everywhere.
func writeAuthErr(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

// NewUserAuthMiddleware returns the correct auth middleware for the configured
// AuthMode.
//   - stub: accepts unsigned "uid@name@role" tokens (dev only, logs WARN)
//   - jwt: validates RS256 JWTs from octo-auth-server via JWKS
//   - remote: deprecated, panics on startup
func NewUserAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	switch cfg.AuthMode {
	case config.AuthModeStub:
		log.Printf("WARN: auth running in STUB mode — 'token: uid@name@role' headers are accepted without verification. DO NOT use in production.")
		return stubUserAuth()
	case config.AuthModeJWT:
		return jwtAuth(cfg.JWKSURL, cfg.Audience)
	case config.AuthModeRemote:
		panic("auth: remote mode is deprecated — use AUTH_MODE=jwt with octo-auth-server")
	default:
		panic("auth: unknown AuthMode " + string(cfg.AuthMode))
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

// jwtAuth validates RS256 JWTs issued by octo-auth-server. It uses the
// octo-auth sdk-go middleware which fetches JWKS, caches keys, and validates
// JWT signature + expiry + audience locally (zero network call on cache hit).
func jwtAuth(jwksURL, audience string) gin.HandlerFunc {
	mw := middleware.New(middleware.Config{
		JWKSURL:  jwksURL,
		Audience: audience,
	})
	// Start JWKS cache background refresh.
	if err := mw.StartBackground(context.Background()); err != nil {
		log.Fatalf("auth: failed to start JWKS cache: %v", err)
	}
	log.Printf("INFO: auth running in JWT mode — JWKS from %s, audience=%s", jwksURL, audience)
	return mw.GinJWT()
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
