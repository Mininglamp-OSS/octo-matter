package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Config holds auth middleware configuration.
type Config struct {
	OctoIMURL string // octoim base URL
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
	SpaceID   string `json:"space_id"`
}

// AuthMiddleware authenticates requests by calling octoim's verify API.
// Supports two auth paths:
//   - User: "token" header → POST /v1/auth/verify
//   - Bot:  "Authorization: Bearer <bot_token>" → POST /v1/auth/verify-bot
//
// On success, injects into gin context:
//   - "uid", "name", "role" — caller identity
//   - "related_uids" — [self, owned_bots...] or [self, owner] for visibility
// verifyCache caches auth verify results to avoid calling octoim on every request.
// It bounds memory via periodic eviction of expired entries and a hard cap.
type verifyCache struct {
	mu      sync.RWMutex
	entries map[string]verifyCacheEntry
}

const verifyCacheMaxSize = 10000

type verifyCacheEntry struct {
	result   interface{}
	expireAt time.Time
}

func newVerifyCache() *verifyCache {
	c := &verifyCache{entries: make(map[string]verifyCacheEntry)}
	go c.evictLoop()
	return c
}

// evictLoop removes expired entries every 5 minutes to prevent unbounded growth.
func (c *verifyCache) evictLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.expireAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}

func (c *verifyCache) get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expireAt) {
		return nil, false
	}
	return e.result, true
}

func (c *verifyCache) set(key string, result interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= verifyCacheMaxSize {
		// Hard cap reached — clear all to prevent unbounded growth.
		c.entries = make(map[string]verifyCacheEntry)
	}
	c.entries[key] = verifyCacheEntry{result: result, expireAt: time.Now().Add(ttl)}
}

func AuthMiddleware(cfg Config) gin.HandlerFunc {
	client := &http.Client{Timeout: 5 * time.Second}
	cache := newVerifyCache()

	return func(c *gin.Context) {
		// Check for Bot auth first
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			botToken := strings.TrimPrefix(authHeader, "Bearer ")
			handleBotAuth(c, client, cfg.OctoIMURL, botToken, cache)
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
		handleUserAuth(c, client, cfg.OctoIMURL, token, cache)
	}
}

func handleUserAuth(c *gin.Context, client *http.Client, baseURL, token string, cache *verifyCache) {
	// Check cache
	if cached, ok := cache.get("user:" + token); ok {
		result := cached.(*verifyTokenResp)
		c.Set("uid", result.UID)
		c.Set("name", result.Name)
		c.Set("role", result.Role)
		relatedUIDs := []string{result.UID}
		for _, bot := range result.OwnedBots {
			relatedUIDs = append(relatedUIDs, bot.UID)
		}
		c.Set("related_uids", relatedUIDs)
		c.Next()
		return
	}

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

	relatedUIDs := []string{result.UID}
	for _, bot := range result.OwnedBots {
		relatedUIDs = append(relatedUIDs, bot.UID)
	}
	c.Set("related_uids", relatedUIDs)

	// Cache for 60s
	cache.set("user:"+token, &result, 60*time.Second)

	c.Next()
}

func handleBotAuth(c *gin.Context, client *http.Client, baseURL, botToken string, cache *verifyCache) {
	// Check cache
	if cached, ok := cache.get("bot:" + botToken); ok {
		result := cached.(*verifyBotResp)
		c.Set("uid", result.BotUID)
		c.Set("name", result.BotName)
		c.Set("role", "bot")
		if result.OwnerUID != "" {
			c.Set("owner_uid", result.OwnerUID)
		}
		if result.OwnerName != "" {
			c.Set("owner_name", result.OwnerName)
		}
		if result.SpaceID != "" {
			c.Set("space_id", result.SpaceID)
		}
		relatedUIDs := []string{result.BotUID}
		c.Set("related_uids", relatedUIDs)
		c.Next()
		return
	}

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
	if result.OwnerUID != "" {
		c.Set("owner_uid", result.OwnerUID)
	}
	if result.OwnerName != "" {
		// Stored so notification handlers can render the owner's name when
		// the bot acts on behalf of its owner (LLM-mediated timeline path).
		c.Set("owner_name", result.OwnerName)
	}
	if result.SpaceID != "" {
		c.Set("space_id", result.SpaceID)
	}

	// Build related UIDs: [self] only. Owner-side visibility of bot matters
	// is handled via the user-auth path's owned_bots expansion.
	relatedUIDs := []string{result.BotUID}
	c.Set("related_uids", relatedUIDs)

	cache.set("bot:"+botToken, &result, 60*time.Second)

	c.Next()
}

// SpaceMiddleware reads X-Space-Id header and validates membership
// by calling octoim's public API (token is forwarded).
func SpaceMiddleware(octoIMURL string) gin.HandlerFunc {
	client := &http.Client{Timeout: 5 * time.Second}
	cache := newSpaceCache()

	return func(c *gin.Context) {
		if _, exists := c.Get("space_id"); exists {
			c.Next()
			return
		}
		spaceID := c.GetHeader("X-Space-Id")
		if spaceID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "VALIDATION_ERROR", "message": "missing X-Space-Id header"},
			})
			return
		}

		// Validate via octoim public API
		if octoIMURL != "" {
			token := c.GetHeader("token")
			if token == "" {
				c.Set("space_id", spaceID)
				c.Next()
				return
			}

			cacheKey := fmt.Sprintf("%s:%s", spaceID, token[:min(len(token), 16)])
			if ok, found := cache.get(cacheKey); found {
				if !ok {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
						"error": gin.H{"code": "SPACE_FORBIDDEN", "message": "not a member of this space"},
					})
					return
				}
				c.Set("space_id", spaceID)
				c.Next()
				return
			}

			req, _ := http.NewRequest("GET", octoIMURL+"/v1/space/"+spaceID, nil)
			req.Header.Set("token", token)
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("SpaceMiddleware: octoim space check failed: %v", err)
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{"code": "UPSTREAM_ERROR", "message": "space verification service unavailable"},
				})
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				log.Printf("SpaceMiddleware: octoim space check returned status %d", resp.StatusCode)
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{"code": "UPSTREAM_ERROR", "message": "space verification service unavailable"},
				})
				return
			}
			cache.set(cacheKey, true, 60*time.Second)
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

// --- Simple in-memory cache for Space membership ---

const spaceCacheMaxSize = 10000

type spaceCache struct {
	mu      sync.RWMutex
	entries map[string]spaceCacheEntry
}

type spaceCacheEntry struct {
	ok       bool
	expireAt time.Time
}

func newSpaceCache() *spaceCache {
	c := &spaceCache{entries: make(map[string]spaceCacheEntry)}
	go c.evictLoop()
	return c
}

func (c *spaceCache) evictLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.expireAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}

func (c *spaceCache) get(key string) (bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expireAt) {
		return false, false
	}
	return e.ok, true
}

func (c *spaceCache) set(key string, ok bool, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= spaceCacheMaxSize {
		c.entries = make(map[string]spaceCacheEntry)
	}
	c.entries[key] = spaceCacheEntry{ok: ok, expireAt: time.Now().Add(ttl)}
}
