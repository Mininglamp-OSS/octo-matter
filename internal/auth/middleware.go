package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Config holds auth middleware configuration.
type Config struct {
	DmworkIMURL string // dmworkim base URL
}

// --- verify API response types ---

type ownedBot struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

type verifyTokenResp struct {
	UID       string     `json:"uid"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	OwnedBots []ownedBot `json:"owned_bots"`
}

type verifyBotResp struct {
	BotUID    string `json:"bot_uid"`
	BotName   string `json:"bot_name"`
	OwnerUID  string `json:"owner_uid"`
	OwnerName string `json:"owner_name"`
}

// AuthMiddleware authenticates requests by calling dmworkim's verify API.
// Supports two auth paths:
//   - User: "token" header → POST /v1/auth/verify
//   - Bot:  "Authorization: Bearer <bot_token>" → POST /v1/auth/verify-bot
//
// On success, injects into gin context:
//   - "uid", "name", "role" — caller identity
//   - "related_uids" — [self, owned_bots...] or [self, owner] for visibility
func AuthMiddleware(cfg Config) gin.HandlerFunc {
	client := &http.Client{Timeout: 5 * time.Second}

	return func(c *gin.Context) {
		// Check for Bot auth first
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			botToken := strings.TrimPrefix(authHeader, "Bearer ")
			handleBotAuth(c, client, cfg.DmworkIMURL, botToken)
			return
		}

		// User token auth
		token := c.GetHeader("token")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": "missing token or Authorization header"},
			})
			return
		}
		handleUserAuth(c, client, cfg.DmworkIMURL, token)
	}
}

func handleUserAuth(c *gin.Context, client *http.Client, baseURL, token string) {
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := client.Post(baseURL+"/v1/auth/verify", "application/json", bytes.NewReader(body))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "AUTH_UNAVAILABLE", "message": "failed to reach auth service"},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"code": "UNAUTHORIZED", "message": "invalid or expired token"},
		})
		return
	}

	var result verifyTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "AUTH_ERROR", "message": "failed to parse auth response"},
		})
		return
	}

	c.Set("uid", result.UID)
	c.Set("name", result.Name)
	c.Set("role", result.Role)

	// Build related UIDs: [self, owned_bot1, owned_bot2, ...]
	relatedUIDs := []string{result.UID}
	for _, bot := range result.OwnedBots {
		relatedUIDs = append(relatedUIDs, bot.UID)
	}
	c.Set("related_uids", relatedUIDs)

	c.Next()
}

func handleBotAuth(c *gin.Context, client *http.Client, baseURL, botToken string) {
	body, _ := json.Marshal(map[string]string{"bot_token": botToken})
	resp, err := client.Post(baseURL+"/v1/auth/verify-bot", "application/json", bytes.NewReader(body))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "AUTH_UNAVAILABLE", "message": "failed to reach auth service"},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"code": "UNAUTHORIZED", "message": "invalid bot token"},
		})
		return
	}

	var result verifyBotResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "AUTH_ERROR", "message": "failed to parse auth response"},
		})
		return
	}

	c.Set("uid", result.BotUID)
	c.Set("name", result.BotName)
	c.Set("role", "bot")

	// Build related UIDs: [self, owner]
	relatedUIDs := []string{result.BotUID}
	if result.OwnerUID != "" {
		relatedUIDs = append(relatedUIDs, result.OwnerUID)
	}
	c.Set("related_uids", relatedUIDs)

	c.Next()
}

// SpaceMiddleware reads X-Space-ID header and validates membership
// by calling dmworkim's public API (token is forwarded).
func SpaceMiddleware(dmworkIMURL string) gin.HandlerFunc {
	client := &http.Client{Timeout: 5 * time.Second}

	return func(c *gin.Context) {
		if _, exists := c.Get("space_id"); exists {
			c.Next()
			return
		}
		spaceID := c.GetHeader("X-Space-ID")
		if spaceID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "VALIDATION_ERROR", "message": "missing X-Space-ID header"},
			})
			return
		}

		// Validate via dmworkim public API
		if dmworkIMURL != "" {
			token := c.GetHeader("token")
			if token != "" {
				req, _ := http.NewRequest("GET", dmworkIMURL+"/v1/space/"+spaceID, nil)
				req.Header.Set("token", token)
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
						c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
							"error": gin.H{"code": "SPACE_FORBIDDEN", "message": "not a member of this space"},
						})
						return
					}
				}
			}
		}

		c.Set("space_id", spaceID)
		c.Next()
	}
}

// GetRelatedUIDs extracts related UIDs from gin context.
func GetRelatedUIDs(c *gin.Context) []string {
	if v, exists := c.Get("related_uids"); exists {
		if uids, ok := v.([]string); ok && len(uids) > 0 {
			return uids
		}
	}
	uid, _ := c.Get("uid")
	if s, ok := uid.(string); ok && s != "" {
		return []string{s}
	}
	return nil
}
