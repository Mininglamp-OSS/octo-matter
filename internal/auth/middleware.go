package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Mininglamp-OSS/octo-matter/internal/config"
)

func writeAuthErr(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

// NewAuthMiddleware returns the auth middleware. No mode selection — it always
// does dual-path routing:
//   - "Authorization: Bot <robot_id>/<app_key>" → bot verification
//   - "token" header → user token verification
//
// Both paths call octo-auth internal endpoints.
func NewAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	client := &http.Client{Timeout: 10 * time.Second}
	baseURL := strings.TrimRight(cfg.AuthURL, "/")
	internalKey := cfg.InternalKey

	log.Printf("INFO: auth middleware — verifying via %s (user: token header, bot: Authorization header)", baseURL)

	return func(c *gin.Context) {
		// Route based on header presence.
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bot ") {
			handleBotAuth(c, client, baseURL, internalKey, authHeader)
			return
		}
		handleUserAuth(c, client, baseURL, internalKey)
	}
}

// --- User token verification ---

type userVerifyRequest struct {
	Token string `json:"token"`
}

type userVerifyResponse struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func handleUserAuth(c *gin.Context, client *http.Client, baseURL, internalKey string) {
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

// --- Bot token verification ---

type botVerifyRequest struct {
	BotToken string `json:"bot_token"`
}

type botVerifyResponse struct {
	RobotID   string `json:"robot_id"`
	RobotName string `json:"robot_name"`
	SpaceID   string `json:"space_id"`
}

func handleBotAuth(c *gin.Context, client *http.Client, baseURL, internalKey, authHeader string) {
	botToken, _ := strings.CutPrefix(authHeader, "Bot ")
	if botToken == "" {
		writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "empty bot token")
		return
	}

	robotID, appKey, found := strings.Cut(botToken, "/")
	if !found || robotID == "" || appKey == "" {
		writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bot token format, expected: <robot_id>/<app_key>")
		return
	}

	verifyURL := baseURL + "/internal/v1/auth/verify-bot"
	body, _ := json.Marshal(botVerifyRequest{BotToken: "bf_" + robotID + "_" + appKey})
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
		writeAuthErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid bot credentials")
		return
	}

	var botResp botVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&botResp); err != nil {
		writeAuthErr(c, http.StatusInternalServerError, "AUTH_ERROR", "failed to parse auth response")
		return
	}

	name := botResp.RobotName
	if name == "" {
		name = robotID
	}
	c.Set("uid", botResp.RobotID)
	c.Set("name", name)
	c.Set("role", "bot")

	spaceID := botResp.SpaceID
	if spaceID == "" {
		spaceID = c.GetHeader("X-Space-ID")
	}
	if spaceID == "" {
		writeAuthErr(c, http.StatusBadRequest, "SPACE_REQUIRED", "bot space could not be resolved; provide X-Space-ID header")
		return
	}
	c.Set("space_id", spaceID)

	c.Next()
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
			writeAuthErr(c, http.StatusBadRequest, "VALIDATION_ERROR", "missing X-Space-ID header")
			return
		}
		c.Set("space_id", spaceID)
		c.Next()
	}
}
