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
// API key prefix uk_ + ≥32 random chars (strict prefix dispatch per auth-decisions plan §4).
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

	// v3.3.1 §B.1' (lml2468 + caster state-space scan): explicit signal
	// from server that ?include=context took effect (server >= v2).
	// Distinguishes "server returned empty spaces" (caller has zero
	// memberships) from "pre-v2 server omitted the field" — needed so
	// applyUserResult can deterministically gate ctx keys instead of
	// using `len > 0` which collapses both into the same fall-through.
	// Mirrors fleet's same-named field (v3 §4.5) and server's
	// authVerifyTokenResp.ContextIncluded (server commit d354cc4).
	ContextIncluded bool `json:"context_included"`

	// v2 auth-relations work: populated by server when middleware passes
	// ?include=context. Used to enforce X-Space-Id membership and per-bot
	// ownership against server-validated data instead of client headers.
	Spaces           []string            `json:"spaces,omitempty"`
	OwnedBotsBySpace map[string][]string `json:"owned_bots_by_space,omitempty"`
}

type verifyBotResp struct {
	BotUID    string `json:"bot_uid"`
	BotName   string `json:"bot_name"`
	OwnerUID  string `json:"owner_uid"`
	OwnerName string `json:"owner_name"`
	SpaceID   string `json:"space_id"`
}

// verifyAPIKeyResp mirrors POST /v1/auth/verify-api-key on server
// (auth-decisions plan §3). The endpoint returns the user + bound space; when
// middleware passes ?include=context the response also carries owned_bots
// keyed by space (always a single-key map for api_key — it's bound to
// exactly one space).
type verifyAPIKeyResp struct {
	UID     string `json:"uid"`
	SpaceID string `json:"space_id"`
	// v3.3.1 §B.1: same signal as verifyTokenResp, paired with the
	// server-side authVerifyAPIKeyResp.ContextIncluded (server d354cc4).
	ContextIncluded bool                `json:"context_included"`
	OwnedBots       map[string][]string `json:"owned_bots,omitempty"`
}

// AuthMiddleware authenticates requests by calling octoim's verify API.
// Supports three auth paths (auth-decisions plan §4):
//   - User:    "token" header → POST /v1/auth/verify (session)
//   - Bot:     "Authorization: Bearer bf_<token>" → POST /v1/auth/verify-bot
//   - APIKey:  "Authorization: Bearer uk_<key>"   → POST /v1/auth/verify-api-key (daemon)
//
// Prefix dispatch is strict (HasPrefix + min length). Bearer headers
// without uk_ or bf_ prefix are rejected outright (Phase 4 tightening; the
// pre-v4 JWT path was removed and no longer falls through to server verify-bot).
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
			// Decisions 1+2 Phase 4: non-uk_/bf_ Bearer → 401 outright.
			// The legacy JWT path was removed; we no longer fall through
			// to let server verify-bot reject as a catch-all.
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
		applyUserResult(c, result)
		return
	}

	body, _ := json.Marshal(map[string]string{"token": token})
	// ?include=context asks server for spaces + owned_bots_by_space so the
	// handler layer can enforce X-Space-Id membership + bot ownership
	// against server-validated data instead of client headers.
	resp, err := client.Post(baseURL+"/v1/auth/verify?include=context", "application/json", bytes.NewReader(body))
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

	applyUserResult(c, &result)
	cache.set("user:"+token, &result, 60*time.Second)
}

// applyUserResult injects verified user context into gin ctx. Shared by
// cache-hit and fresh-fetch paths so both code paths stay in sync.
func applyUserResult(c *gin.Context, result *verifyTokenResp) {
	c.Set("uid", result.UID)
	c.Set("name", result.Name)
	c.Set("role", result.Role)
	c.Set("auth_kind", AuthKindSession)

	relatedUIDs := []string{result.UID}
	for _, bot := range result.OwnedBots {
		relatedUIDs = append(relatedUIDs, bot.UID)
	}
	c.Set("related_uids", relatedUIDs)

	// v3.3.1 §B.1' (caster state-space scan, three-round): ALWAYS set
	// verify_context_included + spaces + owned map when server confirmed
	// v2 (ContextIncluded=true), even if Spaces/OwnedBotsBySpace are nil
	// or empty. Pre-v3.3.1 the guard was `len > 0`, which collapsed two
	// cases:
	//   - pre-v2 server omitted the fields (nil after decode)
	//   - v2 server returned `[]` / `{}` (caller has zero memberships or
	//     zero owned bots — legitimate, common for new users)
	// Both fall into the same fall-through branch where downstream
	// handlers see "no ctx key" and skip ownership checks (fail-open for
	// the few entry points that branch on `if owned != nil`). v3 §4.5
	// landed the same fix in fleet via ContextIncluded; v3.3.1 ports it
	// to matter so the apply pair (apiKey + user) is symmetric.
	if result.ContextIncluded {
		c.Set("verify_context_included", true)
		spaces := result.Spaces
		if spaces == nil {
			spaces = []string{}
		}
		c.Set("spaces", spaces)
		owned := result.OwnedBotsBySpace
		if owned == nil {
			owned = map[string][]string{}
		}
		c.Set("owned_bots_by_space", owned)
	}
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
// decision 2's trust model pushes cross-user isolation down to business
// SQL WHERE owner_uid=? guards.
func handleAPIKeyAuth(c *gin.Context, client *http.Client, baseURL, apiKey string, cache *verifyCache) {
	if cached, ok := cache.get("apikey:" + apiKey); ok {
		result := cached.(*verifyAPIKeyResp)
		applyAPIKeyResult(c, result)
		return
	}

	body, _ := json.Marshal(map[string]string{"api_key": apiKey})
	// ?include=context asks server for owned_bots map so handlers can
	// enforce per-bot ownership (e.g. ClaimNextForBots restricting
	// bot_uids to owned set, issue #75).
	resp, err := client.Post(baseURL+"/v1/auth/verify-api-key?include=context", "application/json", bytes.NewReader(body))
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
	// v3.3.1 §B.1 (lml2468 + Jerry-Xin three-round P0 + caster state-
	// space scan): symmetric fix with applyUserResult above. The previous
	// `len(r.OwnedBots) > 0` collapsed two distinct cases — nil (pre-v2
	// server omitted the field) and empty `{}` (v2 server, caller owns
	// zero bots in this space) — into the same fall-through where the
	// ctx key stays unset and ListBotTasksForDaemon fail-opens (its
	// `if owned != nil` guard skips and ClaimNextForBot[s] runs with
	// caller-supplied bot_uid). ContextIncluded is the explicit signal:
	// when true, set the ctx key even if the map is empty so the
	// downstream "caller owns no bots, deny" branch reaches.
	if r.ContextIncluded {
		c.Set("verify_context_included", true)
		owned := r.OwnedBots
		if owned == nil {
			owned = map[string][]string{}
		}
		c.Set("owned_bots_by_space", owned)
	}
}

// RequireKind enforces that the authenticated caller's auth_kind matches
// one of the allowed kinds (auth-decisions plan §4 Endpoint authz matrix).
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
