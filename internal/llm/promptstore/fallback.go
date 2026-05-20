package promptstore

import (
	"context"
	"errors"
	"fmt"
	"log"
)

// FallbackStore composes two Stores so a remote primary (e.g.
// LangfuseStore) can degrade to a local fallback (e.g. EmbedStore) on
// any error. The intent is that a Langfuse outage never blocks an LLM
// call — the worst case is "we served the embedded prompt for a few
// minutes" rather than "extraction returned 500".
//
// A degradation event is logged once per fallback-served call so an
// operator can correlate elevated LLM-error metrics with prompt-source
// switches without enabling debug logging.
type FallbackStore struct {
	Primary  Store
	Fallback Store
}

// NewFallback returns a FallbackStore. A nil Primary is allowed and
// causes Get to delegate straight to Fallback — convenient in cmd/main.go
// where the remote provider is gated on env vars.
func NewFallback(primary, fallback Store) *FallbackStore {
	return &FallbackStore{Primary: primary, Fallback: fallback}
}

// Get returns the primary store's prompt when available; otherwise it
// logs the degradation and delegates to the fallback. If both fail, the
// returned error wraps both via errors.Join so errors.Is finds either.
func (s *FallbackStore) Get(ctx context.Context, name string) (Prompt, error) {
	if s.Primary == nil {
		return s.Fallback.Get(ctx, name)
	}
	p, err := s.Primary.Get(ctx, name)
	if err == nil {
		return p, nil
	}
	log.Printf("promptstore: primary failed for %q (%v); falling back", name, err)
	fp, ferr := s.Fallback.Get(ctx, name)
	if ferr != nil {
		return Prompt{}, errors.Join(
			fmt.Errorf("primary: %w", err),
			fmt.Errorf("fallback: %w", ferr),
		)
	}
	return fp, nil
}

var _ Store = (*FallbackStore)(nil)
