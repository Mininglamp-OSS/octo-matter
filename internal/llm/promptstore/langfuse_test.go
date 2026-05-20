package promptstore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// langfuseTextResponse is the minimal JSON shape returned by
// GET /api/public/v2/prompts/:name for a text-type prompt.
const langfuseTextResponse = `{
	"name": "extract_matter",
	"version": 7,
	"type": "text",
	"prompt": "system body {{.Var}}",
	"labels": ["production"],
	"config": {},
	"tags": []
}`

func newToolFS() fstest.MapFS {
	return fstest.MapFS{
		"extract_matter.md": &fstest.MapFile{Data: []byte("ignored body")},
		"extract_matter.tool.json": &fstest.MapFile{Data: []byte(
			`{"type":"function","function":{"name":"extract_matter","description":"d","parameters":{"type":"object"}}}`,
		)},
	}
}

// TestLangfuseStore_GetReturnsTextPrompt covers the happy path: a text-type
// prompt is decoded into a Prompt whose Body comes from Langfuse and whose
// Tool comes from the injected tool source (kept in code, not editable via
// Langfuse).
func TestLangfuseStore_GetReturnsTextPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, langfuseTextResponse)
	}))
	defer srv.Close()

	tools := NewEmbed(newToolFS())
	store := NewLangfuse(srv.URL, "pk", "sk", WithLangfuseToolSource(tools))

	p, err := store.Get(context.Background(), "extract_matter")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.Name != "extract_matter" {
		t.Errorf("Name=%q, want extract_matter", p.Name)
	}
	if p.Version != "langfuse:7" {
		t.Errorf("Version=%q, want langfuse:7", p.Version)
	}
	if p.Body != "system body {{.Var}}" {
		t.Errorf("Body=%q, want langfuse body", p.Body)
	}
	if p.Tool.Function.Name != "extract_matter" {
		t.Errorf("Tool.Function.Name=%q, want extract_matter (from tool source)", p.Tool.Function.Name)
	}
}

// TestLangfuseStore_SendsBasicAuthAndLabel pins the wire contract: the
// request must carry Basic auth (base64(pk:sk)) and label query, and target
// the documented /api/public/v2/prompts/:name path.
func TestLangfuseStore_SendsBasicAuthAndLabel(t *testing.T) {
	var (
		gotAuth   string
		gotPath   string
		gotLabel  string
		gotAccept string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		gotLabel = r.URL.Query().Get("label")
		fmt.Fprint(w, langfuseTextResponse)
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk",
		WithLangfuseLabel("staging"),
		WithLangfuseToolSource(NewEmbed(newToolFS())),
	)
	if _, err := store.Get(context.Background(), "extract_matter"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("pk:sk"))
	if gotAuth != wantAuth {
		t.Errorf("Authorization=%q, want %q", gotAuth, wantAuth)
	}
	if gotPath != "/api/public/v2/prompts/extract_matter" {
		t.Errorf("path=%q, want /api/public/v2/prompts/extract_matter", gotPath)
	}
	if gotLabel != "staging" {
		t.Errorf("label query=%q, want staging", gotLabel)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept=%q, want application/json", gotAccept)
	}
}

// TestLangfuseStore_404ReturnsNotFound is the contract that lets
// FallbackStore distinguish "prompt missing" (fall back to embed) from
// "Langfuse broken" (also fall back, but separately diagnosable).
func TestLangfuseStore_404ReturnsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk", WithLangfuseToolSource(NewEmbed(newToolFS())))
	_, err := store.Get(context.Background(), "extract_matter")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

// TestLangfuseStore_5xxReturnsError verifies server errors are NOT
// reported as NotFound — they signal an outage and the fallback chain
// should still kick in, but observability must distinguish the two.
func TestLangfuseStore_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk", WithLangfuseToolSource(NewEmbed(newToolFS())))
	_, err := store.Get(context.Background(), "extract_matter")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("5xx must not be reported as NotFound, got %v", err)
	}
}

// TestLangfuseStore_ChatTypeRejected guards Step 2's scope: only text-type
// prompts are supported until chat templates are designed in.
func TestLangfuseStore_ChatTypeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name":"x","version":1,"type":"chat","prompt":[{"role":"system","content":"hi"}]}`)
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk", WithLangfuseToolSource(NewEmbed(newToolFS())))
	_, err := store.Get(context.Background(), "extract_matter")
	if err == nil {
		t.Fatal("expected error for chat-type, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("chat-type must surface as a typed error, not NotFound: %v", err)
	}
}

// TestLangfuseStore_CachesWithinTTL verifies the in-memory cache: a second
// Get within the TTL must not hit the upstream server. Justifies the
// short-TTL design choice in the issue.
func TestLangfuseStore_CachesWithinTTL(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, langfuseTextResponse)
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk",
		WithLangfuseCacheTTL(time.Hour),
		WithLangfuseToolSource(NewEmbed(newToolFS())),
	)
	for i := 0; i < 3; i++ {
		if _, err := store.Get(context.Background(), "extract_matter"); err != nil {
			t.Fatalf("Get #%d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("server hits=%d, want 1 (TTL caching broken)", got)
	}
}

// TestLangfuseStore_CacheExpiresAfterTTL ensures the cache is bounded:
// stale entries get refreshed so a Langfuse-side prompt update propagates
// within roughly the TTL window without a process restart.
func TestLangfuseStore_CacheExpiresAfterTTL(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, langfuseTextResponse)
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk",
		WithLangfuseCacheTTL(10*time.Millisecond),
		WithLangfuseToolSource(NewEmbed(newToolFS())),
	)
	if _, err := store.Get(context.Background(), "extract_matter"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := store.Get(context.Background(), "extract_matter"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("server hits=%d, want 2 (cache did not expire)", got)
	}
}

// TestLangfuseStore_MissingToolSourceErrors enforces that tool schemas are
// always sourced in-code. A misconfigured store (no tool source) must fail
// loudly rather than silently returning a Prompt with a zero-value Tool.
func TestLangfuseStore_MissingToolSourceErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, langfuseTextResponse)
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk") // no WithLangfuseToolSource
	_, err := store.Get(context.Background(), "extract_matter")
	if err == nil {
		t.Fatal("expected error when tool source is nil, got nil")
	}
}

// TestLangfuseStore_ToolSourceErrorPropagates ensures tool-side failures
// surface — a Langfuse body without its paired tool schema is unusable, so
// the call must not return a partially-populated Prompt.
func TestLangfuseStore_ToolSourceErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, langfuseTextResponse)
	}))
	defer srv.Close()

	// Empty FS — tool source has no extract_matter.tool.json.
	emptyTools := NewEmbed(fstest.MapFS{})
	store := NewLangfuse(srv.URL, "pk", "sk", WithLangfuseToolSource(emptyTools))
	_, err := store.Get(context.Background(), "extract_matter")
	if err == nil {
		t.Fatal("expected tool-source error, got nil")
	}
}

// TestLangfuseStore_ContextCancelStopsRequest confirms the store honors
// the caller's context — a slow Langfuse must not block the request
// goroutine past the caller's deadline.
func TestLangfuseStore_ContextCancelStopsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	store := NewLangfuse(srv.URL, "pk", "sk", WithLangfuseToolSource(NewEmbed(newToolFS())))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := store.Get(ctx, "extract_matter")
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}
