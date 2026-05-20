//go:build integration

// Integration tests for LangfuseStore against a real Langfuse instance.
// Skipped from the default test run via the `integration` build tag.
//
// Usage:
//
//	LANGFUSE_HOST=https://prod-langfuse.xmingai.com \
//	LANGFUSE_PUBLIC_KEY=pk-... \
//	LANGFUSE_SECRET_KEY=sk-... \
//	go test -tags=integration ./internal/llm/promptstore/ -run TestLangfuseIntegration -v
//
// Assumes the two production prompts (`extract_matter`, `extract_progress`)
// are present on the target Langfuse instance with label "production",
// synced from internal/llm/prompts/*.md.

package promptstore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm/prompts"
)

func integrationConfig(t *testing.T) (host, pk, sk string) {
	t.Helper()
	host = os.Getenv("LANGFUSE_HOST")
	if host == "" {
		host = os.Getenv("LANGFUSE_BASE_URL")
	}
	pk = os.Getenv("LANGFUSE_PUBLIC_KEY")
	sk = os.Getenv("LANGFUSE_SECRET_KEY")
	if host == "" || pk == "" || sk == "" {
		t.Skip("LANGFUSE_HOST / LANGFUSE_PUBLIC_KEY / LANGFUSE_SECRET_KEY not set; skipping integration test")
	}
	return host, pk, sk
}

// TestLangfuseIntegration_FetchExtractMatter confirms the field mapping
// against a real Langfuse response on a real prompt: Body comes from
// upstream, Version is "langfuse:<n>", Tool comes from the in-code
// source. Render against representative data must succeed without
// missingkey errors.
func TestLangfuseIntegration_FetchExtractMatter(t *testing.T) {
	host, pk, sk := integrationConfig(t)
	embed := NewEmbed(prompts.FS)
	store := NewLangfuse(host, pk, sk,
		WithLangfuseToolSource(embed),
		WithLangfuseTimeout(10*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p, err := store.Get(ctx, "extract_matter")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Name != "extract_matter" {
		t.Errorf("Name=%q, want extract_matter", p.Name)
	}
	if !strings.HasPrefix(p.Version, "langfuse:") {
		t.Errorf("Version=%q, want langfuse: prefix", p.Version)
	}
	if p.Body == "" {
		t.Fatal("Body empty — Langfuse returned no content")
	}
	if p.Tool.Function.Name != "extract_matter" {
		t.Errorf("Tool.Function.Name=%q, want extract_matter (from embed source)", p.Tool.Function.Name)
	}
}

// TestLangfuseIntegration_404ReturnsNotFound verifies the live
// 404-mapping contract using a name that definitely does not exist.
// FallbackStore relies on this to distinguish missing prompts from
// transport failures.
func TestLangfuseIntegration_404ReturnsNotFound(t *testing.T) {
	host, pk, sk := integrationConfig(t)
	store := NewLangfuse(host, pk, sk,
		WithLangfuseToolSource(NewEmbed(prompts.FS)),
		WithLangfuseTimeout(10*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := store.Get(ctx, "does_not_exist_octo_matter_xyz")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

// TestLangfuseIntegration_RealPromptsByteEquivalent pins the cross-source
// drift contract: prompts synced to Langfuse from internal/llm/prompts/*.md
// must match the embedded copy byte-for-byte. Drift is the exact failure
// FallbackStore cannot detect at runtime (both paths "work") and would
// cause silent prompt behavior changes depending on Langfuse availability.
// If this fails, re-sync the affected prompt and bump its Langfuse
// version with a clear commitMessage.
func TestLangfuseIntegration_RealPromptsByteEquivalent(t *testing.T) {
	host, pk, sk := integrationConfig(t)
	embed := NewEmbed(prompts.FS)
	lf := NewLangfuse(host, pk, sk,
		WithLangfuseToolSource(embed),
		WithLangfuseTimeout(10*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, name := range []string{"extract_matter", "extract_progress"} {
		ep, err := embed.Get(ctx, name)
		if err != nil {
			t.Fatalf("embed %q: %v", name, err)
		}
		lp, err := lf.Get(ctx, name)
		if err != nil {
			t.Fatalf("langfuse %q: %v", name, err)
		}
		if ep.Body != lp.Body {
			t.Errorf("%q body diverged: embed %d bytes, langfuse %d bytes (re-sync from internal/llm/prompts/%s.md)",
				name, len(ep.Body), len(lp.Body), name)
		}
	}
}

// TestLangfuseIntegration_FallbackChainServesEmbedOnRemoteMiss confirms
// the production wiring: when Langfuse 404s for an unknown name, the
// chain transparently returns the embed copy (which also has it, in
// this case extract_matter). Exercises FallbackStore end-to-end against
// a real Langfuse without taking the network down.
func TestLangfuseIntegration_FallbackChainServesEmbedOnRemoteMiss(t *testing.T) {
	host, pk, sk := integrationConfig(t)
	embed := NewEmbed(prompts.FS)
	primary := NewLangfuse(host, pk, sk,
		WithLangfuseToolSource(embed),
		WithLangfuseTimeout(10*time.Second),
	)
	chain := NewFallback(primary, embed)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Happy path: present in both → primary wins.
	p, err := chain.Get(ctx, "extract_matter")
	if err != nil {
		t.Fatalf("chain Get extract_matter: %v", err)
	}
	if !strings.HasPrefix(p.Version, "langfuse:") {
		t.Errorf("Version=%q, want langfuse: prefix (primary should win when both have it)", p.Version)
	}

	// Fallback path: missing in Langfuse (404) AND missing in embed →
	// both error. Confirms errors.Join surfaces both causes.
	_, err = chain.Get(ctx, "does_not_exist_octo_matter_xyz")
	if err == nil {
		t.Fatal("expected error when neither store has the prompt")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected errors.Is(err, ErrNotFound), got %v", err)
	}
}
