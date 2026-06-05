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

// Auth kind constants — injected into gin context by AuthMiddleware so
// downstream RequireKind / handlers can disambiguate the caller type.
const (
	AuthKindSession = "session" // browser session token (header: token: <session>)
	AuthKindBot     = "bot"     // bot_token (header: Authorization: Bearer bf_<token>)
	AuthKindAPIKey  = "apikey"  // daemon api_key (header: Authorization: Bearer uk_<key>)
)

// API key / bot token prefix constants.
//
// API key prefix uk_ + ≥32 random chars (合并 plan §4 prefix 严格性).
// We require strict HasPrefix match + minimum length so a Bearer with
// "uk_" + empty string can't sneak through the middleware to land on
// the server side.
const (
	apiKeyPrefix    = "uk_"
	apiKeyMinLength = 35 // "uk_" + 32-char body
	botTokenPrefix  = "bf_"
)

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

// verifyAPIKeyResp mirrors POST /v1/auth/verify-api-key on server
// (合并 plan §3). The endpoint only returns the user + bound space —
// daemon-facing handlers derive everything else from owner_uid filtering.
type verifyAPIKeyResp struct {
	UID     string `json:"uid"`
	SpaceID string `json:"space_id"`
}

// AuthMiddleware authenticates requests by calling octoim's verify API.
// Supports three auth paths (合并 plan §4 三协议):
//   - User:    "token" header → POST /v1/auth/verify (session)
//   - Bot:     "Authorization: Bearer bf_<token>" → POST /v1/auth/verify-bot
//   - APIKey:  "Authorization: Bearer uk_<key>"   → POST /v1/auth/verify-api-key (daemon)
//
// Prefix dispatch is strict (HasPrefix + min length). Bearer headers
// without uk_ or bf_ prefix are rejected outright (Phase 4 收紧, 不再
// fall through to bot path letting server verify-bot reject).
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
		// Bearer auth: dispatch by prefix (api_key vs bot_token).
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			// API key path: strict uk_ prefix + min length to avoid
			// "uk_" with empty body sneaking through.
			if strings.HasPrefix(token, apiKeyPrefix) && len(token) >= apiKeyMinLength {
				handleAPIKeyAuth(c, client, cfg.OctoIMURL, token, cache)
				return
			}
			// Bot token: strict bf_ prefix.
			if strings.HasPrefix(token, botTokenPrefix) {
				handleBotAuth(c, client, cfg.OctoIMURL, token, cache)
				return
			}
			// 合并 plan 决策一+二 Phase 4: 非 uk_/bf_ 的 Bearer 直接 401
			// (旧 JWT 路径已删, 不再 fall through 让 server verify-bot 兜底).
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": "Bearer token must start with uk_ or bf_"},
			})
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
		c.Set("auth_kind", AuthKindSession)
		relatedUIDs := []string{result.UID}
		for _, bot := range result.OwnedBots {
			relatedUIDs = append(relatedUIDs, bot.UID)
		}
		c.Set("related_uids", relatedUIDs)
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
	c.Set("auth_kind", AuthKindSession)

	relatedUIDs := []string{result.UID}
	for _, bot := range result.OwnedBots {
		relatedUIDs = append(relatedUIDs, bot.UID)
	}
	c.Set("related_uids", relatedUIDs)

	// Cache for 60s
	cache.set("user:"+token, &result, 60*time.Second)

}

func handleBotAuth(c *gin.Context, client *http.Client, baseURL, botToken string, cache *verifyCache) {
	// Check cache
	if cached, ok := cache.get("bot:" + botToken); ok {
		result := cached.(*verifyBotResp)
		c.Set("uid", result.BotUID)
		c.Set("name", result.BotName)
		c.Set("role", "bot")
		c.Set("auth_kind", AuthKindBot)
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
	c.Set("auth_kind", AuthKindBot)
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

}

// handleAPIKeyAuth verifies a daemon api_key by calling server's
// /v1/auth/verify-api-key endpoint and caches the result for 60s.
//
// Injects uid + space_id + auth_kind="apikey" only — api_key callers
// (daemon / runtime / agent processes) don't need name/role/related_uids;
// 决策二信任模型把跨 user 隔离推到业务 SQL WHERE owner_uid=?.
func handleAPIKeyAuth(c *gin.Context, client *http.Client, baseURL, apiKey string, cache *verifyCache) {
	if cached, ok := cache.get("apikey:" + apiKey); ok {
		result := cached.(*verifyAPIKeyResp)
		applyAPIKeyResult(c, result)
		return
	}

	body, _ := json.Marshal(map[string]string{"api_key": apiKey})
	resp, err := client.Post(baseURL+"/v1/auth/verify-api-key", "application/json", bytes.NewReader(body))
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
			"error": gin.H{"code": "UNAUTHORIZED", "message": "invalid api_key"},
		})
		return
	}

	var result verifyAPIKeyResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "AUTH_ERROR", "message": "failed to parse auth response"},
		})
		return
	}

	applyAPIKeyResult(c, &result)
	cache.set("apikey:"+apiKey, &result, 60*time.Second)

}

func applyAPIKeyResult(c *gin.Context, r *verifyAPIKeyResp) {
	c.Set("uid", r.UID)
	c.Set("space_id", r.SpaceID)
	c.Set("auth_kind", AuthKindAPIKey)
}

// RequireKind enforces that the authenticated caller's auth_kind matches
// one of the allowed kinds (合并 plan §4 Endpoint 鉴权矩阵).
//
// Returns 403 (not 401 — credential is valid but the endpoint disallows
// this caller type) on mismatch. Must be mounted after AuthMiddleware:
//
//	r.Group("/api/v1/internal/bot-tasks",
//	    auth.AuthMiddleware(cfg),
//	    auth.RequireKind(auth.AuthKindAPIKey))
//
// Phase 2 ships the helper; Phase 3B is where individual endpoint groups
// adopt it as daemon-facing handlers migrate off DualAuth onto AuthMiddleware.
func RequireKind(allowed ...string) gin.HandlerFunc {
	set := make(map[string]struct{}, len(allowed))
	for _, k := range allowed {
		set[k] = struct{}{}
	}
	return func(c *gin.Context) {
		raw, _ := c.Get("auth_kind")
		kind, _ := raw.(string)
		if _, ok := set[kind]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"code":    "AUTH_KIND_NOT_ALLOWED",
					"message": fmt.Sprintf("endpoint requires one of: %s", strings.Join(allowed, ", ")),
				},
			})
			return
		}
		c.Next()
	}
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
