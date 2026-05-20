package promptstore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
)

// stubStore is a tiny Store used to drive FallbackStore tests without
// spinning up an HTTP server. Each call increments calls so tests can
// assert "primary skipped" / "fallback invoked" semantics.
type stubStore struct {
	prompt Prompt
	err    error
	calls  int32
}

func (s *stubStore) Get(_ context.Context, name string) (Prompt, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.err != nil {
		return Prompt{}, s.err
	}
	p := s.prompt
	if p.Name == "" {
		p.Name = name
	}
	return p, nil
}

func newOKStub(version string) *stubStore {
	return &stubStore{
		prompt: Prompt{Version: version, Body: "body-" + version, Tool: llm.Tool{Type: "function"}},
	}
}

// TestFallback_PrimaryOkSkipsFallback proves the happy path does not
// touch the fallback at all — important because the fallback is the
// embed store and reading embed.FS on every call would be wasteful.
func TestFallback_PrimaryOkSkipsFallback(t *testing.T) {
	primary := newOKStub("primary")
	fallback := newOKStub("fallback")
	store := NewFallback(primary, fallback)

	p, err := store.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Version != "primary" {
		t.Errorf("Version=%q, want primary", p.Version)
	}
	if atomic.LoadInt32(&fallback.calls) != 0 {
		t.Errorf("fallback called %d times, want 0", fallback.calls)
	}
}

// TestFallback_PrimaryNotFoundUsesFallback covers the canonical degrade
// path: Langfuse returns 404 (prompt not yet published there), so the
// embed copy answers the call.
func TestFallback_PrimaryNotFoundUsesFallback(t *testing.T) {
	primary := &stubStore{err: ErrNotFound}
	fallback := newOKStub("embed")
	store := NewFallback(primary, fallback)

	p, err := store.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Version != "embed" {
		t.Errorf("Version=%q, want embed", p.Version)
	}
	if atomic.LoadInt32(&primary.calls) != 1 {
		t.Errorf("primary called %d times, want 1", primary.calls)
	}
}

// TestFallback_PrimaryArbitraryErrUsesFallback covers the other
// motivating case: a 5xx, timeout, or DNS failure on Langfuse must not
// break extraction — embed should answer.
func TestFallback_PrimaryArbitraryErrUsesFallback(t *testing.T) {
	primary := &stubStore{err: errors.New("network down")}
	fallback := newOKStub("embed")
	store := NewFallback(primary, fallback)

	p, err := store.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Version != "embed" {
		t.Errorf("Version=%q, want embed", p.Version)
	}
}

// TestFallback_BothErrReturnsPrimaryErr documents the failure mode: if
// both stores fail, the primary error is surfaced (with the fallback
// error attached via errors.Join) so observability picks up both causes.
func TestFallback_BothErrReturnsPrimaryErr(t *testing.T) {
	primaryErr := errors.New("primary broken")
	fallbackErr := errors.New("fallback broken")
	primary := &stubStore{err: primaryErr}
	fallback := &stubStore{err: fallbackErr}
	store := NewFallback(primary, fallback)

	_, err := store.Get(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, primaryErr) {
		t.Errorf("err does not wrap primary: %v", err)
	}
	if !errors.Is(err, fallbackErr) {
		t.Errorf("err does not wrap fallback: %v", err)
	}
}

// TestFallback_NilPrimaryDelegates lets callers wire a single store
// transparently — useful in cmd/main.go where Langfuse may be absent in
// dev. A nil primary collapses to the fallback without a special case
// at the call site.
func TestFallback_NilPrimaryDelegates(t *testing.T) {
	fallback := newOKStub("embed")
	store := NewFallback(nil, fallback)

	p, err := store.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Version != "embed" {
		t.Errorf("Version=%q, want embed", p.Version)
	}
}
