// Package octoim wraps the octoim public REST API used by this service
// for cross-cutting authorization checks (channel membership today; potentially
// more in the future). It transparently caches lookups and forwards the
// caller's user token, mirroring the pattern in internal/auth.SpaceMiddleware.
package octoim

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout     = 5 * time.Second
	membershipCacheTTL = 60 * time.Second
	membershipCacheMax = 10000
	cacheEvictInterval = 5 * time.Minute
)

// Client is the octoim API client. Construct via NewClient.
type Client struct {
	baseURL string
	http    *http.Client
	cache   *membershipCache

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// memberQueryResp models the relevant subset of the
// GET /v1/groups/:group_no/members/:uid response.
type memberQueryResp struct {
	Exists bool `json:"exists"`
}

// NewClient returns a Client that talks to baseURL with the given per-request
// timeout. timeout <= 0 falls back to 5 seconds.
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
		cache:   newMembershipCache(),
		done:    make(chan struct{}),
	}
	c.wg.Add(1)
	go c.evictLoop()
	return c
}

// Close stops the background cache eviction goroutine. Safe to call multiple
// times. After Close, IsChannelMember continues to work (no eviction; entries
// expire on TTL but stay in memory until process exit).
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.wg.Wait()
	})
}

func (c *Client) evictLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(cacheEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.cache.evict()
		}
	}
}

// IsChannelMember reports whether ANY uid in userIDs is a current member of
// channelID, as resolved by octoim. The caller's user token authorizes the
// lookup itself (IM requires the caller to be a member of channelID).
//
//   - (true, nil) — at least one uid is confirmed member
//   - (false, nil) — all uids confirmed non-members; or 4xx response (caller
//     not authorized to query, or IM upstream DB issue — both surface as 4xx
//     and we conservatively treat as "not a member")
//   - (false, err) — 5xx, network error, or decode failure; caller should
//     map to a 503 ServiceUnavailable response
//
// Empty channelID, empty token, or empty userIDs short-circuit to (false, nil).
func (c *Client) IsChannelMember(ctx context.Context, token, channelID string, userIDs []string) (bool, error) {
	if token == "" || channelID == "" || len(userIDs) == 0 {
		return false, nil
	}
	key := buildCacheKey(token, channelID, userIDs)
	if v, ok := c.cache.get(key); ok {
		return v, nil
	}
	for _, uid := range userIDs {
		if uid == "" {
			continue
		}
		hit, err := c.queryOne(ctx, token, channelID, uid)
		if err != nil {
			// Do not cache transient failures.
			return false, err
		}
		if hit {
			c.cache.set(key, true)
			return true, nil
		}
	}
	c.cache.set(key, false)
	return false, nil
}

func (c *Client) queryOne(ctx context.Context, token, channelID, uid string) (bool, error) {
	// PathEscape both segments — channelID and uid come from external sources
	// (request payload, IM identity); a "/" or "?" in either would let the
	// caller redirect the request to a different IM endpoint.
	endpoint := fmt.Sprintf("%s/v1/groups/%s/members/%s",
		c.baseURL, url.PathEscape(channelID), url.PathEscape(uid))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("octoim: build request: %w", err)
	}
	req.Header.Set("token", token)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("octoim: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return false, fmt.Errorf("octoim: upstream status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		// 4xx covers IM's "no permission" (caller not in group) and "DB error"
		// (IM-side internal failure) — both 400 with only a Chinese msg field
		// to distinguish. Per PR#34 review thread we conservatively treat both
		// as "not a member" rather than parsing localized strings.
		return false, nil
	}
	var body memberQueryResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("octoim: decode response: %w", err)
	}
	return body.Exists, nil
}

// buildCacheKey returns an opaque cache key for the membership lookup.
// The token is hashed (SHA-256) so:
//   - the raw token never lives in the cache map / heap dump
//   - two distinct tokens that share a prefix don't collide on the same
//     cached authorization decision (PR #34 review r4259102520)
func buildCacheKey(token, channelID string, userIDs []string) string {
	sorted := make([]string, 0, len(userIDs))
	for _, u := range userIDs {
		if u == "" {
			continue
		}
		sorted = append(sorted, u)
	}
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]) + "|" + channelID + "|" + strings.Join(sorted, ",")
}

// --- in-memory cache ---

type membershipCache struct {
	mu      sync.RWMutex
	entries map[string]membershipEntry
}

type membershipEntry struct {
	member   bool
	expireAt time.Time
}

func newMembershipCache() *membershipCache {
	return &membershipCache{entries: make(map[string]membershipEntry)}
}

func (c *membershipCache) get(key string) (bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expireAt) {
		return false, false
	}
	return e.member, true
}

func (c *membershipCache) set(key string, member bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= membershipCacheMax {
		// Hard cap: clear all rather than scanning for an LRU victim. We expect
		// to never approach the cap in practice; this is a guard rail.
		c.entries = make(map[string]membershipEntry)
	}
	c.entries[key] = membershipEntry{
		member:   member,
		expireAt: time.Now().Add(membershipCacheTTL),
	}
}

func (c *membershipCache) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expireAt) {
			delete(c.entries, k)
		}
	}
}
