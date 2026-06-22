package auth

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	octoauth "github.com/Mininglamp-OSS/octo-auth/sdk-go/auth"
	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/gin-gonic/gin"
)

// Config holds auth middleware configuration.
type Config struct {
	OctoIMURL string // octo-server (formerly octoim) base URL — also the SDK's ServerURL
}

// AuthMiddleware authenticates requests via the octo-auth SDK by
// calling Client.VerifyUser / VerifyBot / VerifyAPIKey directly (not
// the SDK's gin.Middleware wrapper). The wrapper-style middleware
// calls c.Next() at the end of its body, which advances the entire
// gin chain — meaning my "copy SDK keys → legacy keys" code would run
// AFTER the downstream handler had already executed (mochashanyao
// review F1 on #86). Direct Client.Verify* calls give full control
// over chain sequencing while preserving the SDK's value: SHA-256-
// keyed LRU cache, strict prefix validation, fail-closed-on-5xx,
// anti-enumeration error mapping.
//
// Pre-SDK behaviour preserved:
//   - Token extraction: Authorization Bearer (any prefix) first; legacy
//     "token" header fallback used by octo-web / iOS / Android (project
//     doc §14.3).
//   - role="bot" for bot-authenticated requests (matter handlers
//     discriminate on this string).
//   - owner_uid + owner_name copied for bots (LLM-mediated
//     bot-on-behalf-of-owner notifications need both).
//   - related_uids = [self, owned_bots...] for users, [self, owner]
//     for bots.
//   - User-language preference promoted into i18n via
//     i18n.PromoteUserLanguage — for sessions from VerifyUserResp.Language,
//     for bots from VerifyBotResp.Language (owner's preference).
//   - Cache TTL: 60s (SDK default).
func AuthMiddleware(cfg Config) gin.HandlerFunc {
	client := getOrInitSDKClient(cfg.OctoIMURL)

	return func(c *gin.Context) {
		token, kind, ok := extractToken(c)
		if !ok {
			i18n.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", i18n.KeyAuthMissingToken, nil, nil)
			return
		}

		switch kind {
		case sdkKindAPIKey:
			handleAPIKey(c, client, token)
		case sdkKindBot:
			handleBot(c, client, token)
		default: // session token
			handleUser(c, client, token)
		}
	}
}

// Token-kind sentinel for extractToken (matches the SDK's classification).
type tokenKind int

const (
	sdkKindSession tokenKind = iota
	sdkKindBot
	sdkKindAPIKey
)

// extractToken pulls (token, kind, true) from the request. Returns
// (_, _, false) when no parseable token is present.
func extractToken(c *gin.Context) (string, tokenKind, bool) {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if tok == "" {
			return "", 0, false
		}
		switch {
		case strings.HasPrefix(tok, "uk_"):
			return tok, sdkKindAPIKey, true
		case strings.HasPrefix(tok, "bf_"), strings.HasPrefix(tok, "app_"):
			return tok, sdkKindBot, true
		default:
			return tok, sdkKindSession, true
		}
	}
	if tok := strings.TrimSpace(c.GetHeader("token")); tok != "" {
		return tok, sdkKindSession, true
	}
	return "", 0, false
}

func handleUser(c *gin.Context, client *octoauth.Client, token string) {
	resp, err := client.VerifyUser(c.Request.Context(), token, true /* includeContext */)
	if err != nil {
		respondSDKError(c, err)
		return
	}
	c.Set("uid", resp.UID)
	c.Set("name", resp.Name)
	c.Set("role", resp.Role)
	related := []string{resp.UID}
	for _, b := range resp.OwnedBots {
		related = append(related, b.UID)
	}
	c.Set("related_uids", related)
	if resp.Language != "" {
		i18n.PromoteUserLanguage(c, resp.Language)
	}
	// Set SDK ctx keys too so SpaceMiddlewareWithSDK's IsContextIncluded
	// + GetVerifiedSpaces check works (Jerry-Xin P0 review on #87:
	// without these set, the SDK gate is a silent no-op). We bypass
	// SDK's injectUserContext because we call Client.VerifyUser
	// directly, so we have to set the SDK keys ourselves.
	if resp.ContextIncluded {
		c.Set(octoauth.CtxKeyContextIncluded, true)
		c.Set(octoauth.CtxKeyVerifiedSpaces, resp.Spaces)
		c.Set(octoauth.CtxKeyOwnedBotsBySpace, resp.OwnedBotsBySpace)
	}
	c.Next()
}

func handleBot(c *gin.Context, client *octoauth.Client, token string) {
	resp, err := client.VerifyBot(c.Request.Context(), token)
	if err != nil {
		respondSDKError(c, err)
		return
	}
	// matter handlers (resp.go:140-160 et al.) discriminate on
	// role=="bot" — preserve that string exactly.
	c.Set("uid", resp.BotUID)
	c.Set("name", resp.BotName)
	c.Set("role", "bot")
	if resp.OwnerUID != "" {
		c.Set("owner_uid", resp.OwnerUID)
	}
	if resp.OwnerName != "" {
		// LLM-mediated bot-on-behalf-of-owner notifications
		// (internal/handler/resp.go:101-105) need the owner's display
		// name; legacy authVerifyBot returned it.
		c.Set("owner_name", resp.OwnerName)
	}
	related := []string{resp.BotUID}
	if resp.OwnerUID != "" {
		related = append(related, resp.OwnerUID)
	}
	c.Set("related_uids", related)
	if resp.Language != "" {
		// Bot path: language is the owner's preference.
		i18n.PromoteUserLanguage(c, resp.Language)
	}
	// SDK ctx keys for the bot path. For Scope="space" bots the
	// server-verified binding is the only allowed space; mirror what
	// the SDK's injectBotContext does so SpaceMiddlewareWithSDK
	// enforces against the bot binding (yujiawei P0 review on
	// octo-auth#2 fix).
	if resp.SpaceID != "" {
		c.Set("space_id", resp.SpaceID)
		if resp.Scope == "space" {
			c.Set(octoauth.CtxKeyContextIncluded, true)
			c.Set(octoauth.CtxKeyVerifiedSpaces, []string{resp.SpaceID})
		}
	}
	c.Next()
}

func handleAPIKey(c *gin.Context, client *octoauth.Client, token string) {
	resp, err := client.VerifyAPIKey(c.Request.Context(), token, true /* includeContext */)
	if err != nil {
		respondSDKError(c, err)
		return
	}
	c.Set("uid", resp.UID)
	if resp.SpaceID != "" {
		c.Set("space_id", resp.SpaceID)
	}
	// SDK ctx keys for the API key path so SpaceMiddlewareWithSDK
	// enforces against the verify-context. Built from
	// OwnedBotsBySpace's keys (the set of spaces the key has access
	// to) — mirrors what the SDK's injectAPIKeyContext does for
	// daemon callers.
	if resp.ContextIncluded {
		c.Set(octoauth.CtxKeyContextIncluded, true)
		c.Set(octoauth.CtxKeyOwnedBotsBySpace, resp.OwnedBotsBySpace)
		spaces := make([]string, 0, len(resp.OwnedBotsBySpace))
		for k := range resp.OwnedBotsBySpace {
			spaces = append(spaces, k)
		}
		// Also include the bound space (if any) so SpaceMiddlewareWithSDK
		// passes when X-Space-Id matches the binding even when the bot
		// list is empty.
		if resp.SpaceID != "" {
			found := false
			for _, s := range spaces {
				if s == resp.SpaceID {
					found = true
					break
				}
			}
			if !found {
				spaces = append(spaces, resp.SpaceID)
			}
		}
		c.Set(octoauth.CtxKeyVerifiedSpaces, spaces)
	}
	c.Next()
}

// respondSDKError maps a SDK sentinel error to matter's i18n envelope.
// Anti-enumeration: any "token bad" reason → single 401 with
// KeyAuthInvalidToken.
func respondSDKError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, octoauth.ErrTokenMissing):
		i18n.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", i18n.KeyAuthMissingToken, nil, nil)
	case errors.Is(err, octoauth.ErrTokenInvalid), errors.Is(err, octoauth.ErrKindMismatch):
		i18n.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", i18n.KeyAuthInvalidToken, nil, nil)
	case errors.Is(err, octoauth.ErrBotUnavailable):
		i18n.RespondError(c, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", i18n.KeyAuthUnavailable, nil, nil)
	case errors.Is(err, octoauth.ErrUpstreamUnavailable):
		i18n.RespondError(c, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", i18n.KeyAuthUnavailable, nil, nil)
	default:
		i18n.RespondError(c, http.StatusUnauthorized, "UNAUTHORIZED", i18n.KeyAuthInvalidToken, nil, nil)
	}
}

// sdkClientSingleton is a process-wide octo-auth/sdk-go Client. Reused
// across AuthMiddleware invocations because constructing a Client
// allocates an LRU cache and the SDK is designed for one-Client-per-
// service-lifetime. Keyed on the resolved ServerURL so a test that
// constructs two middlewares against different mock servers each get
// their own Client.
var (
	sdkClientsMu sync.Mutex
	sdkClients   = map[string]*octoauth.Client{}
)

func getOrInitSDKClient(serverURL string) *octoauth.Client {
	sdkClientsMu.Lock()
	defer sdkClientsMu.Unlock()
	if c, ok := sdkClients[serverURL]; ok {
		return c
	}
	c, err := octoauth.New(octoauth.Options{
		ServerURL: serverURL,
		// Default Options are SDK-sane: 5s timeout, 2 retries,
		// 60s cache TTL, 10k LRU, noop metrics (matter doesn't run
		// Prometheus today; can opt in by setting Collector).
	})
	if err != nil {
		// Construction can only fail on empty ServerURL — log loudly
		// and return a placeholder that fails every request rather
		// than nil so callers don't NPE. The placeholder construction
		// uses a hardcoded non-empty ServerURL so it cannot itself
		// fail; if octoauth.New ever grows new failure modes, panic
		// rather than return nil and crash callers far from the
		// misconfiguration.
		log.Printf("auth: SDK Client construction failed for %q: %v", serverURL, err)
		var pErr error
		c, pErr = octoauth.New(octoauth.Options{ServerURL: "http://invalid"})
		if pErr != nil {
			log.Panicf("auth: SDK Client placeholder construction failed: %v", pErr)
		}
	}
	sdkClients[serverURL] = c
	return c
}

// SpaceMiddleware reads X-Space-Id header and validates membership by
// calling octo-server's /v1/space/{id} public API. This is distinct
// from the SDK's RequireSpaceMember (which checks the X-Space-Id
// against the verify response's spaces[] list) — both fail-closed but
// answer slightly different questions.
//
// PR-C3 in the parent project may layer RequireSpaceMember on top.
// PR-C1 keeps this code intact to minimise blast radius.
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
			i18n.RespondError(c, http.StatusBadRequest, "VALIDATION_ERROR", i18n.KeySpaceMissingHeader, nil, nil)
			return
		}

		if octoIMURL != "" {
			token := c.GetHeader("token")
			if token == "" {
				c.Set("space_id", spaceID)
				c.Next()
				return
			}

			cacheKey := fmt.Sprintf("%s:%s", spaceID, token[:minInt(len(token), 16)])
			if ok, found := cache.get(cacheKey); found {
				if !ok {
					i18n.RespondError(c, http.StatusForbidden, "SPACE_FORBIDDEN", i18n.KeySpaceForbidden, nil, nil)
					return
				}
				c.Set("space_id", spaceID)
				c.Next()
				return
			}

			req, err := http.NewRequest("GET", octoIMURL+"/v1/space/"+spaceID, nil)
			if err != nil {
				log.Printf("SpaceMiddleware: build request: %v", err)
				i18n.RespondError(c, http.StatusServiceUnavailable, "UPSTREAM_ERROR", i18n.KeySpaceUnavailable, nil, nil)
				return
			}
			req.Header.Set("token", token)
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("SpaceMiddleware: octoim space check failed: %v", err)
				i18n.RespondError(c, http.StatusServiceUnavailable, "UPSTREAM_ERROR", i18n.KeySpaceUnavailable, nil, nil)
				return
			}
			if cerr := resp.Body.Close(); cerr != nil {
				log.Printf("SpaceMiddleware: resp.Body.Close: %v", cerr)
			}
			if resp.StatusCode != http.StatusOK {
				log.Printf("SpaceMiddleware: octoim space check returned status %d", resp.StatusCode)
				i18n.RespondError(c, http.StatusServiceUnavailable, "UPSTREAM_ERROR", i18n.KeySpaceUnavailable, nil, nil)
				return
			}
			cache.set(cacheKey, true, 60*time.Second)
		}

		c.Set("space_id", spaceID)
		c.Next()
	}
}

// GetRelatedUIDs extracts related UIDs from gin context. Public helper
// retained for backward compatibility with handlers that don't want to
// pull in the SDK directly.
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

// RequireSpaceMember returns the octo-auth SDK's fail-closed
// X-Space-Id ↔ verified-spaces decorator. It is intended to chain
// AFTER both AuthMiddleware and SpaceMiddleware: AuthMiddleware
// populates the verify-context with the user's authorised spaces[];
// SpaceMiddleware extracts X-Space-Id and asks octo-server "is this
// space alive?"; THIS decorator answers the orthogonal question "is
// this caller a member of that space?" without an extra round-trip.
//
// Together the three give defense-in-depth on space access:
//   - AuthMiddleware: who are you?
//   - SpaceMiddleware: is the space valid?
//   - RequireSpaceMember: are you in that space?
//
// When octo-server is too old to return verify-context
// (context_included=false), the SDK's decorator falls back to
// log-warn-and-pass — SpaceMiddleware's per-request membership probe
// still gates the call.
//
// PR-C3 of the parent project (Stage A epic) introduces this as the
// preferred fail-closed mechanism going forward; SpaceMiddleware will
// likely be retired in a future PR once all deployments are on a
// context-aware octo-server.
func RequireSpaceMember(cfg Config) gin.HandlerFunc {
	client := getOrInitSDKClient(cfg.OctoIMURL)
	return client.RequireSpaceMember()
}

// SpaceMiddlewareWithSDK is the recommended composite Space gate
// post-PR-C3: it chains the SDK's RequireSpaceMember (fast, in-memory
// check against verify-context spaces[]) BEFORE the legacy
// SpaceMiddleware (per-request octo-server membership probe). The
// in-memory check rejects most forgeries without an extra HTTP call;
// the per-request probe is the fallback for the pre-context-aware
// octo-server compatibility window and remains the source of truth
// for "space exists" semantics.
//
// Use this in SetupRouter instead of bare SpaceMiddleware; the
// composite is fail-closed on X-Space-Id-not-in-verified-spaces when
// the verify context is available, fail-loud-but-correct on
// pre-v1 octo-server.
func SpaceMiddlewareWithSDK(cfg Config) gin.HandlerFunc {
	sdkGate := RequireSpaceMember(cfg)
	legacyGate := SpaceMiddleware(cfg.OctoIMURL)
	return func(c *gin.Context) {
		sdkGate(c)
		if c.IsAborted() {
			return
		}
		legacyGate(c)
	}
}

// --- Simple in-memory cache for Space membership ---
//
// Retained for SpaceMiddleware's octo-server /v1/space/{id} call. The
// auth verify cache (which used to also live here) is now inside the
// octo-auth SDK.

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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
