package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

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
//   - bot: validates Bot tokens (robot_id:app_key) against Octo IM server
//   - remote: deprecated, panics on startup
func NewUserAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	switch cfg.AuthMode {
	case config.AuthModeStub:
		log.Printf("WARN: auth running in STUB mode — 'token: uid@name@role' headers are accepted without verification. DO NOT use in production.")
		return stubUserAuth()
	case config.AuthModeJWT:
		return jwtAuth(cfg.JWKSURL, cfg.Audience)
	case config.AuthModeBot:
		return botAuth(cfg.AuthURL)
	case config.AuthModeRemote:
		panic("auth: remote mode is deprecated — use AUTH_MODE=jwt or AUTH_MODE=bot")
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

// jwtAuth validates RS256 JWTs issued by octo-auth-server.
func jwtAuth(jwksURL, audience string) gin.HandlerFunc {
	mw := middleware.New(middleware.Config{
		JWKSURL:  jwksURL,
		Audience: audience,
	})
	if err := mw.StartBackground(context.Background()); err != nil {
		log.Fatalf("auth: failed to start JWKS cache: %v", err)
	}
	log.Printf("INFO: auth running in JWT mode — JWKS from %s, audience=%s", jwksURL, audience)
	return mw.GinJWT()
}

// botAuth validates Bot tokens by calling Octo IM server's robot events endpoint.
// Token format: "Authorization: Bot <robot_id>/<app_key>"
// The middleware calls GET <authURL>/robots/<robot_id>/<app_key>/events to validate.
// If Octo IM returns 200, the bot is authenticated. The robot_id is used as uid.
func botAuth(authURL string) gin.HandlerFunc {
	client := &http.Client{Timeout: 10 * time.Second}
	baseURL := strings.TrimRight(authURL, "/")

	log.Printf("INFO: auth running in BOT mode — validating against %s", baseURL)

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authorization header")
			return
		}

		// Parse "Bot <robot_id>/<app_key>"
		botToken, ok := strings.CutPrefix(authHeader, "Bot ")
		if !ok || botToken == "" {
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authorization format, expected: Bot <robot_id>/<app_key>")
			return
		}

		robotID, appKey, found := strings.Cut(botToken, "/")
		if !found || robotID == "" || appKey == "" {
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bot token format, expected: <robot_id>/<app_key>")
			return
		}

		// Validate against Octo IM by calling the events endpoint (lightweight GET)
		validateURL := fmt.Sprintf("%s/v1/robots/%s/%s/events", baseURL, robotID, appKey)
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, validateURL, nil)
		if err != nil {
			writeAuthErr(c, http.StatusInternalServerError, "AUTH_ERROR", "failed to create validation request")
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			writeAuthErr(c, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "failed to reach auth server")
			return
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bot credentials")
			return
		}

		// Bot is authenticated. Set identity in context.
		// robot_id is the bot's username (uid equivalent).
		c.Set("uid", robotID)
		c.Set("name", robotID) // Bot name defaults to robot_id
		c.Set("role", "bot")

		// Try to get space_id from Octo IM response or from a separate call.
		// For MVP, we read X-Space-ID from the request header (set by octo-cli from config).
		// Space validation is done by the repo layer (every query includes space_id).
		c.Next()
	}
}

// BotSpaceMiddleware reads X-Space-ID from the request header for Bot auth mode.
// In JWT mode, space_id comes from the token claims.
// In Bot mode, it comes from the environment (OCTO_SPACE_ID injected by agent runtime).
func BotSpaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if space_id is already set (by JWT middleware)
		if _, exists := c.Get("space_id"); exists {
			c.Next()
			return
		}

		// Fall back to header
		spaceID := c.GetHeader("X-Space-ID")
		if spaceID == "" {
			writeAuthErr(c, http.StatusBadRequest, "VALIDATION_ERROR", "missing X-Space-ID header")
			return
		}
		c.Set("space_id", spaceID)
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

// botInfoFromIM tries to fetch bot info (including space) from Octo IM.
// Not used in MVP — space comes from X-Space-ID header.
func botInfoFromIM(baseURL, robotID, appKey string) (string, string, error) {
	url := fmt.Sprintf("%s/v1/robots/%s/%s/events", baseURL, robotID, appKey)
	resp, err := http.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("bot validation failed: %d", resp.StatusCode)
	}

	var result struct {
		SpaceID string `json:"space_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return robotID, "", nil // no space info available
	}
	return robotID, result.SpaceID, nil
}
