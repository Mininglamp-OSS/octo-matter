// Package auth — daemon JWT middleware for matter (PR-B).
//
// Mirrors octo-fleet/internal/auth/jwt.go: matter pulls octo-server's
// JWKS at startup, caches by kid, refreshes on unknown-kid (10s
// floor), and verifies daemon-scope JWTs locally. No HTTP back to
// server per request.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
)

type JWTClaims struct {
	jwt.Claims
	Scope    string `json:"scope"`
	SpaceID  string `json:"space_id,omitempty"`
	DaemonID string `json:"daemon_id,omitempty"`
}

type JWTVerifier struct {
	jwksURL string
	httpc   *http.Client

	mu                 sync.RWMutex
	keys               map[string]*jose.JSONWebKey
	lastFetch          time.Time
	minRefreshInterval time.Duration
}

func NewJWTVerifier(jwksURL string) *JWTVerifier {
	v := &JWTVerifier{
		jwksURL:            jwksURL,
		httpc:              &http.Client{Timeout: 5 * time.Second},
		keys:               map[string]*jose.JSONWebKey{},
		minRefreshInterval: 10 * time.Second,
	}
	if err := v.refresh(); err != nil {
		log.Printf("[auth-jwt] initial JWKS fetch failed (will retry lazily) url=%s err=%v", jwksURL, err)
	}
	return v
}

func (v *JWTVerifier) refresh() error {
	req, err := http.NewRequest(http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}
	if len(set.Keys) == 0 {
		return errors.New("JWKS empty")
	}
	keys := make(map[string]*jose.JSONWebKey, len(set.Keys))
	for i := range set.Keys {
		k := &set.Keys[i]
		keys[k.KeyID] = k
	}
	v.mu.Lock()
	v.keys = keys
	v.lastFetch = time.Now()
	v.mu.Unlock()
	log.Printf("[auth-jwt] JWKS refreshed (key_count=%d)", len(keys))
	return nil
}

func (v *JWTVerifier) keyForKid(kid string) (*jose.JSONWebKey, error) {
	v.mu.RLock()
	k, ok := v.keys[kid]
	since := time.Since(v.lastFetch)
	v.mu.RUnlock()
	if ok {
		return k, nil
	}
	if since < v.minRefreshInterval {
		return nil, fmt.Errorf("unknown kid %q (cache still fresh)", kid)
	}
	if err := v.refresh(); err != nil {
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown kid %q after refresh", kid)
}

func (v *JWTVerifier) Verify(token string) (*JWTClaims, error) {
	parsed, err := jwt.ParseSigned(token)
	if err != nil {
		return nil, fmt.Errorf("parse jwt: %w", err)
	}
	if len(parsed.Headers) == 0 {
		return nil, errors.New("jwt missing headers")
	}
	kid := parsed.Headers[0].KeyID
	if kid == "" {
		return nil, errors.New("jwt missing kid")
	}
	key, err := v.keyForKid(kid)
	if err != nil {
		return nil, err
	}
	var cl JWTClaims
	if err := parsed.Claims(key.Key, &cl); err != nil {
		return nil, fmt.Errorf("verify claims: %w", err)
	}
	if err := cl.Validate(jwt.Expected{Time: time.Now()}); err != nil {
		return nil, fmt.Errorf("claims expired/invalid: %w", err)
	}
	return &cl, nil
}

// DaemonJWTMiddleware enforces scope=daemon JWT and stuffs claims into
// the gin context (uid, daemon_id, space_id). Use for /v1/internal/
// bot-tasks routes that daemons hit directly.
func DaemonJWTMiddleware(verifier *JWTVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": "missing Bearer token"},
			})
			return
		}
		tok := strings.TrimPrefix(auth, "Bearer ")
		cl, err := verifier.Verify(tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": err.Error()},
			})
			return
		}
		if cl.Scope != "daemon" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "SCOPE_MISMATCH", "message": "daemon scope required"},
			})
			return
		}
		c.Set("uid", cl.Subject)
		c.Set("daemon_id", cl.DaemonID)
		c.Set("space_id", cl.SpaceID)
		c.Set("scope", cl.Scope)
		c.Next()
	}
}
