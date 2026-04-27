package auth

import (
	"bytes"
	"context"
	"encoding/json"
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
//   - bot: validates Bot tokens (robot_id:app_key) against octo-auth
//   - remote: validates IM user tokens via octo-auth verify-user endpoint
//   - mixed: auto-routes based on header ("Authorization: Bot" → bot, "token" → remote)
func NewUserAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	switch cfg.AuthMode {
	case config.AuthModeStub:
		log.Printf("WARN: auth running in STUB mode — 'token: uid@name@role' headers are accepted without verification. DO NOT use in production.")
		return stubUserAuth()
	case config.AuthModeJWT:
		return jwtAuth(cfg.JWKSURL, cfg.Audience)
	case config.AuthModeBot:
		return botAuth(cfg.AuthURL, cfg.InternalKey)
	case config.AuthModeRemote:
		return remoteUserAuth(cfg.AuthURL, cfg.InternalKey)
	case config.AuthModeMixed:
		return mixedAuth(cfg.AuthURL, cfg.InternalKey)
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

// botVerifyRequest is the JSON body sent to POST /v1/auth/verify-bot.
type botVerifyRequest struct {
	BotToken string `json:"bot_token"`
}

// botVerifyResponse is the parsed JSON from a successful verify-bot call.
type botVerifyResponse struct {
	RobotID   string `json:"robot_id"`
	RobotName string `json:"robot_name"`
	SpaceID   string `json:"space_id"`
}

// botAuth validates Bot tokens by calling POST /v1/auth/verify-bot.
// Token format: "Authorization: Bot <robot_id>/<app_key>"
// The middleware posts {"bot_token":"bf_<robot_id>_<app_key>"} to the auth server.
// Only HTTP 200 is treated as success; the response body provides robot info and space_id.
func botAuth(authURL, internalKey string) gin.HandlerFunc {
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

		// Validate via POST /v1/auth/verify-bot with credentials in body (not URL).
		verifyURL := baseURL + "/v1/auth/verify-bot"
		body, _ := json.Marshal(botVerifyRequest{
			BotToken: "bf_" + robotID + "_" + appKey,
		})
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, verifyURL, bytes.NewReader(body))
		if err != nil {
			writeAuthErr(c, http.StatusInternalServerError, "AUTH_ERROR", "failed to create validation request")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if internalKey != "" {
			req.Header.Set("X-Internal-Key", internalKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			writeAuthErr(c, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "failed to reach auth server")
			return
		}
		defer resp.Body.Close()

		// Only 200 is success; everything else is a failure.
		if resp.StatusCode != http.StatusOK {
			io.Copy(io.Discard, resp.Body)
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bot credentials")
			return
		}

		// Parse robot info from response.
		var botResp botVerifyResponse
		if err := json.NewDecoder(resp.Body).Decode(&botResp); err != nil {
			writeAuthErr(c, http.StatusInternalServerError, "AUTH_ERROR", "failed to parse auth response")
			return
		}

		// Set identity in context.
		name := botResp.RobotName
		if name == "" {
			name = robotID
		}
		c.Set("uid", botResp.RobotID)
		c.Set("name", name)
		c.Set("role", "bot")

		// Space resolution: prefer space_id from verify-bot response (auto-resolved),
		// fall back to X-Space-ID header if the auth server did not return one.
		spaceID := botResp.SpaceID
		if spaceID == "" {
			spaceID = c.GetHeader("X-Space-ID")
		}
		if spaceID == "" {
			writeAuthErr(c, http.StatusBadRequest, "SPACE_REQUIRED",
				"bot space could not be resolved; provide X-Space-ID header")
			return
		}
		c.Set("space_id", spaceID)

		c.Next()
	}
}

// TODO: Phase B: implement mixed auth middleware that routes based on header
// presence (Authorization: Bot ... → botAuth, Authorization: Bearer ... → jwtAuth)
// to serve both user and bot simultaneously on the same service instance.

// userVerifyRequest is the JSON body sent to POST /internal/v1/auth/verify-user.
type userVerifyRequest struct {
	Token string `json:"token"`
}

// userVerifyResponse is the parsed JSON from a successful verify-user call.
type userVerifyResponse struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// remoteUserAuth validates IM user tokens by calling octo-auth's verify-user endpoint.
func remoteUserAuth(authURL, internalKey string) gin.HandlerFunc {
	client := &http.Client{Timeout: 10 * time.Second}
	baseURL := strings.TrimRight(authURL, "/")

	log.Printf("INFO: auth running in REMOTE mode — verifying user tokens via %s", baseURL)

	return func(c *gin.Context) {
		token := c.GetHeader("token")
		if token == "" {
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing token header")
			return
		}

		verifyURL := baseURL + "/internal/v1/auth/verify-user"
		body, _ := json.Marshal(userVerifyRequest{Token: token})
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, verifyURL, bytes.NewReader(body))
		if err != nil {
			writeAuthErr(c, http.StatusInternalServerError, "AUTH_ERROR", "failed to create validation request")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if internalKey != "" {
			req.Header.Set("X-Internal-Key", internalKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			writeAuthErr(c, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "failed to reach auth server")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			io.Copy(io.Discard, resp.Body)
			writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid token")
			return
		}

		var userResp userVerifyResponse
		if err := json.NewDecoder(resp.Body).Decode(&userResp); err != nil {
			writeAuthErr(c, http.StatusInternalServerError, "AUTH_ERROR", "failed to parse auth response")
			return
		}

		c.Set("uid", userResp.UID)
		c.Set("name", userResp.Name)
		c.Set("role", userResp.Role)
		c.Next()
	}
}

// mixedAuth routes requests to the correct auth handler based on headers:
//   - "Authorization: Bot ..." → bot auth (verify-bot)
//   - "token" header → remote user auth (verify-user)
func mixedAuth(authURL, internalKey string) gin.HandlerFunc {
	botHandler := botAuth(authURL, internalKey)
	userHandler := remoteUserAuth(authURL, internalKey)

	log.Printf("INFO: auth running in MIXED mode — user (token header) + bot (Authorization: Bot)")

	return func(c *gin.Context) {
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bot ") {
			botHandler(c)
			return
		}
		userHandler(c)
	}
}

// SpaceMiddleware reads the X-Space-ID header and stores it in the gin context.
// Used for user auth (JWT mode). Bot auth mode handles space_id internally.
func SpaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip if space_id is already set (bot auth sets it from X-Space-ID)
		if _, exists := c.Get("space_id"); exists {
			c.Next()
			return
		}
		spaceID := c.GetHeader("X-Space-ID")
		if spaceID == "" {
			writeAuthErr(c, http.StatusBadRequest, "VALIDATION_ERROR", "missing X-Space-ID header")
			return
		}
		c.Set("space_id", spaceID)
		c.Next()
	}
}
