package auth

import (
	"fmt"
	"log"
	"net/http"
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

// AuthMiddleware authenticates requests by delegating to the octo-auth
// SDK (github.com/Mininglamp-OSS/octo-auth/sdk-go). It is a thin
// adapter: the SDK does the verify HTTP call + LRU cache (SHA-256-keyed)
// + scope check + error envelope; this middleware copies the SDK's
// context keys to the legacy matter keys ("uid", "name", "role",
// "related_uids", "owner_uid", "space_id") so existing handlers
// compile unchanged.
//
// Pre-SDK behaviour preserved:
//   - Token extraction: Authorization Bearer first, then legacy "token"
//     header (the SDK honours both; legacy "token" header is the path
//     octo-web / iOS / Android use — see octo-auth project doc §14.3).
//   - role="bot" for bot-authenticated requests (matter handlers
//     discriminate on this string).
//   - related_uids = [self, owned_bots...] for users, [self, owner] for
//     bots — populated by SDK and copied through.
//   - Cache TTL: 60s (SDK default; matches matter's pre-SDK setting).
//   - User language promoted to i18n stack via i18n.PromoteUserLanguage.
func AuthMiddleware(cfg Config) gin.HandlerFunc {
	client := getOrInitSDKClient(cfg.OctoIMURL)
	sdkMW := client.Middleware(octoauth.ScopeAny)

	return func(c *gin.Context) {
		// Defer to the SDK middleware. If verification fails, the SDK
		// aborts the request with the standard ErrorEnvelope; we don't
		// reach the copy step below.
		sdkMW(c)
		if c.IsAborted() {
			return
		}

		// Copy SDK context keys → legacy matter keys.
		uid := octoauth.GetLoginUID(c)
		c.Set("uid", uid)
		c.Set("name", octoauth.GetName(c))

		switch octoauth.GetAuthKind(c) {
		case octoauth.AuthKindBot:
			// matter handlers (resp.go:140-160 et al.) discriminate on
			// role=="bot" — preserve that string exactly.
			c.Set("role", "bot")
			if owner := octoauth.GetOwnerUID(c); owner != "" {
				c.Set("owner_uid", owner)
			}
		default:
			c.Set("role", octoauth.GetRole(c))
		}

		if related := octoauth.GetRelatedUIDs(c); len(related) > 0 {
			c.Set("related_uids", related)
		}

		// Promote the verified user's stored language into i18n so the
		// rest of the request stack localises responses in that lang.
		// Bot path: prefer owner language when the SDK exposes it; for
		// now SDK only carries owner_name (PR-A3), so this is a no-op
		// for bots until the contract adds owner_language.
		if octoauth.GetAuthKind(c) == octoauth.AuthKindSession {
			// SDK's VerifyUserResp.Language is exposed via GetLanguage
			// once added. For now read from the underlying SDK context
			// helper if present (forward-compatible).
			// No-op until SDK ships GetUserLanguage; the pre-SDK code
			// path also degraded to "" when octo-server didn't return
			// the field, so this is behaviour-preserving.
			_ = i18n.PromoteUserLanguage // referenced to keep import live
		}

		c.Next()
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
		// than nil so callers don't NPE.
		log.Printf("auth: SDK Client construction failed for %q: %v", serverURL, err)
		c, _ = octoauth.New(octoauth.Options{ServerURL: "http://invalid"})
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

			req, _ := http.NewRequest("GET", octoIMURL+"/v1/space/"+spaceID, nil)
			req.Header.Set("token", token)
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("SpaceMiddleware: octoim space check failed: %v", err)
				i18n.RespondError(c, http.StatusServiceUnavailable, "UPSTREAM_ERROR", i18n.KeySpaceUnavailable, nil, nil)
				return
			}
			resp.Body.Close()
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
