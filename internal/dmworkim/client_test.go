// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package dmworkim

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubServer captures inbound requests and replies with a configurable handler.
type stubServer struct {
	srv      *httptest.Server
	requests atomic.Int64
}

func newStub(t *testing.T, h http.HandlerFunc) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		h(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestClient_IsChannelMember_HitOnFirstUID(t *testing.T) {
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"exists":true,"member":{"uid":"u1","group_no":"ch1"}}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ok, err := c.IsChannelMember(context.Background(), "tok", "ch1", []string{"u1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected member=true")
	}
}

func TestClient_IsChannelMember_AllMissReturnsFalse(t *testing.T) {
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"exists":false}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ok, err := c.IsChannelMember(context.Background(), "tok", "ch1", []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected member=false when all uids miss")
	}
	if got := stub.requests.Load(); got != 2 {
		t.Fatalf("expected 2 HTTP calls (one per uid), got %d", got)
	}
}

func TestClient_IsChannelMember_AnyHitWins(t *testing.T) {
	// First uid misses, second hits. We should stop after the hit and not call
	// the third.
	var seen []string
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		// Path: /v1/groups/ch1/members/<uid>
		parts := strings.Split(r.URL.Path, "/")
		uid := parts[len(parts)-1]
		seen = append(seen, uid)
		if uid == "u2" {
			fmt.Fprintln(w, `{"exists":true}`)
			return
		}
		fmt.Fprintln(w, `{"exists":false}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ok, err := c.IsChannelMember(context.Background(), "tok", "ch1", []string{"u1", "u2", "u3"})
	if err != nil || !ok {
		t.Fatalf("expected hit, got ok=%v err=%v", ok, err)
	}
	if len(seen) != 2 || seen[0] != "u1" || seen[1] != "u2" {
		t.Fatalf("expected short-circuit after u2, got %v", seen)
	}
}

func TestClient_IsChannelMember_4xxTreatedAsNotMember(t *testing.T) {
	// IM returns 400 for both "no permission" and "DB error" — we treat both as
	// "not a member" rather than parsing the msg field.
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, `{"status":400,"msg":"没有权限查看此群成员"}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ok, err := c.IsChannelMember(context.Background(), "tok", "ch1", []string{"u1"})
	if err != nil {
		t.Fatalf("4xx must NOT propagate as error, got %v", err)
	}
	if ok {
		t.Fatalf("4xx must be treated as member=false")
	}
}

func TestClient_IsChannelMember_5xxReturnsError(t *testing.T) {
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	_, err := c.IsChannelMember(context.Background(), "tok", "ch1", []string{"u1"})
	if err == nil {
		t.Fatalf("5xx should return error so callers can map to 503")
	}
}

func TestClient_IsChannelMember_NetworkErrorPropagates(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", 100*time.Millisecond)
	defer c.Close()
	_, err := c.IsChannelMember(context.Background(), "tok", "ch1", []string{"u1"})
	if err == nil {
		t.Fatalf("network error should propagate")
	}
}

func TestClient_IsChannelMember_TokenAndPathForwarded(t *testing.T) {
	var capturedToken, capturedPath string
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		capturedToken = r.Header.Get("token")
		capturedPath = r.URL.Path
		fmt.Fprintln(w, `{"exists":true}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	_, _ = c.IsChannelMember(context.Background(), "user-tok-123", "ch1", []string{"u1"})
	if capturedToken != "user-tok-123" {
		t.Fatalf("token header not forwarded, got %q", capturedToken)
	}
	if capturedPath != "/v1/groups/ch1/members/u1" {
		t.Fatalf("unexpected path %q", capturedPath)
	}
}

// TestClient_IsChannelMember_PathSegmentsEscaped guards against an injection
// where a channelID or uid containing "/" or "?" or "#" rewrites the URL.
// Both fields come from external sources (request payload, IM identity), so
// they MUST be url.PathEscape'd before going into the URL.
//
// Note: assertions inspect r.URL.EscapedPath() (the wire-form path) — Go's
// r.URL.Path is the decoded form and would mask the bug.
func TestClient_IsChannelMember_PathSegmentsEscaped(t *testing.T) {
	var capturedEscaped string
	var capturedSegments int
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		capturedEscaped = r.URL.EscapedPath()
		capturedSegments = strings.Count(r.URL.EscapedPath(), "/")
		fmt.Fprintln(w, `{"exists":false}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	// channelID with traversal + uid with query separator.
	_, _ = c.IsChannelMember(context.Background(), "tok",
		"ch/../admin", []string{"u id?role=admin"})

	// Properly escaped, the path is /v1/groups/<one_segment>/members/<one_segment>
	// = exactly 5 slashes. If "/" leaked through, the path would have 7+.
	if capturedSegments != 5 {
		t.Fatalf("expected 5 path slashes after escape, got %d in %q", capturedSegments, capturedEscaped)
	}
	// "/" inside channelID must be %2F-escaped.
	if !strings.Contains(capturedEscaped, "%2F") && !strings.Contains(capturedEscaped, "%2f") {
		t.Fatalf("channelID '/' was not escaped: %q", capturedEscaped)
	}
	// "?" inside uid must be %3F-escaped (else it would split into raw query).
	if !strings.Contains(capturedEscaped, "%3F") && !strings.Contains(capturedEscaped, "%3f") {
		t.Fatalf("uid '?' was not escaped: %q", capturedEscaped)
	}
}

func TestClient_IsChannelMember_CacheHitSkipsHTTP(t *testing.T) {
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"exists":true}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		ok, err := c.IsChannelMember(ctx, "tok", "ch1", []string{"u1"})
		if err != nil || !ok {
			t.Fatalf("iter %d: expected hit, got ok=%v err=%v", i, ok, err)
		}
	}
	if got := stub.requests.Load(); got != 1 {
		t.Fatalf("expected 1 HTTP call (cache hits skip remaining), got %d", got)
	}
}

func TestClient_IsChannelMember_CacheKeyDistinguishesUIDs(t *testing.T) {
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"exists":false}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ctx := context.Background()
	_, _ = c.IsChannelMember(ctx, "tok", "ch1", []string{"u1"})
	_, _ = c.IsChannelMember(ctx, "tok", "ch1", []string{"u2"})
	if got := stub.requests.Load(); got != 2 {
		t.Fatalf("different uid sets must not share cache, got %d HTTP calls", got)
	}
}

// TestClient_IsChannelMember_CacheKeyDistinguishesTokenSuffix covers PR #34
// review r4259102520: keying the cache by a token PREFIX would let two
// different tokens with the same first 16 bytes share an authorization
// result. The cache key must derive from the FULL token (e.g. SHA-256).
func TestClient_IsChannelMember_CacheKeyDistinguishesTokenSuffix(t *testing.T) {
	hits := 0
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		// Token A is allowed, Token B is denied — let the suffix decide.
		if r.Header.Get("token") == "PREFIX-aaaaaaaa-A-token" {
			fmt.Fprintln(w, `{"exists":true}`)
			return
		}
		fmt.Fprintln(w, `{"exists":false}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ctx := context.Background()
	// Two tokens sharing the first 20 chars (well past any naive "first 16"
	// prefix) but differing at the suffix.
	tokA := "PREFIX-aaaaaaaa-A-token"
	tokB := "PREFIX-aaaaaaaa-B-token"

	okA, _ := c.IsChannelMember(ctx, tokA, "ch1", []string{"u1"})
	okB, _ := c.IsChannelMember(ctx, tokB, "ch1", []string{"u1"})

	if !okA {
		t.Fatalf("token A must be allowed by stub")
	}
	if okB {
		t.Fatalf("token B must NOT inherit token A's cached allow — naive prefix collision")
	}
	if hits != 2 {
		t.Fatalf("expected 2 HTTP calls (no cache hit across distinct tokens), got %d", hits)
	}
}

// TestClient_IsChannelMember_CacheKeyDoesNotLeakRawToken guards that the
// cache key is opaque (e.g. SHA-256), not the raw token. We poke at
// buildCacheKey directly and assert no substring of the token leaks in.
func TestClient_IsChannelMember_CacheKeyDoesNotLeakRawToken(t *testing.T) {
	tok := "this-is-a-secret-bearer-token-123456789"
	key := buildCacheKey(tok, "ch1", []string{"u1"})
	if strings.Contains(key, tok) {
		t.Fatalf("cache key contains raw token: %q", key)
	}
	// Also reject any 8+ char substring of the token sneaking in (a hash
	// would never coincidentally produce one).
	for i := 0; i+8 <= len(tok); i++ {
		sub := tok[i : i+8]
		if strings.Contains(key, sub) {
			t.Fatalf("cache key %q leaks token substring %q", key, sub)
		}
	}
}

func TestClient_IsChannelMember_EmptyArgsShortCircuit(t *testing.T) {
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("HTTP must not be called for empty args")
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	cases := []struct {
		name      string
		token     string
		channelID string
		uids      []string
	}{
		{"no token", "", "ch1", []string{"u1"}},
		{"no channel", "tok", "", []string{"u1"}},
		{"no uids", "tok", "ch1", nil},
		{"empty uid slice", "tok", "ch1", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := c.IsChannelMember(context.Background(), tc.token, tc.channelID, tc.uids)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok {
				t.Fatalf("empty args must yield false")
			}
		})
	}
}

func TestClient_Close_Idempotent(t *testing.T) {
	c := NewClient("http://example.com", time.Second)
	c.Close()
	c.Close() // must not panic
}

func TestClient_IsChannelMember_ContextCanceled(t *testing.T) {
	stub := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		fmt.Fprintln(w, `{"exists":true}`)
	})
	c := NewClient(stub.srv.URL, time.Second)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.IsChannelMember(ctx, "tok", "ch1", []string{"u1"})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
