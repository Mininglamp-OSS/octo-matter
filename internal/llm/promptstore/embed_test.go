package promptstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmbedStore_GetReturnsBodyAndTool covers the happy path: when both
// {name}.md and {name}.tool.json are present, Get returns a populated
// Prompt with a deterministic content-hash version.
func TestEmbedStore_GetReturnsBodyAndTool(t *testing.T) {
	fsys := fstest.MapFS{
		"draft.md": &fstest.MapFile{Data: []byte("hello {{.Name}}\n")},
		"draft.tool.json": &fstest.MapFile{Data: []byte(`{
			"type": "function",
			"function": {
				"name": "draft",
				"description": "test",
				"parameters": {"type": "object", "properties": {}}
			}
		}`)},
	}
	store := NewEmbed(fsys)
	p, err := store.Get(context.Background(), "draft")
	if err != nil {
		t.Fatalf("Get returned %v, want nil", err)
	}
	if p.Name != "draft" {
		t.Errorf("Name=%q, want draft", p.Name)
	}
	if !strings.HasPrefix(p.Version, "embed:") {
		t.Errorf("Version=%q, want embed: prefix", p.Version)
	}
	if p.Body != "hello {{.Name}}\n" {
		t.Errorf("Body=%q, want raw template", p.Body)
	}
	if p.Tool.Function.Name != "draft" {
		t.Errorf("Tool.Function.Name=%q, want draft", p.Tool.Function.Name)
	}
}

// TestEmbedStore_VersionStableAcrossCalls ensures the content hash is
// deterministic: identical inputs yield identical versions across
// invocations.
func TestEmbedStore_VersionStableAcrossCalls(t *testing.T) {
	fsys := fstest.MapFS{
		"x.md":        &fstest.MapFile{Data: []byte("body")},
		"x.tool.json": &fstest.MapFile{Data: []byte(`{"type":"function","function":{"name":"x","description":"d","parameters":{}}}`)},
	}
	store := NewEmbed(fsys)
	a, err := store.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	b, err := store.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if a.Version != b.Version {
		t.Errorf("Version differs across calls: %q vs %q", a.Version, b.Version)
	}
}

// TestEmbedStore_VersionChangesWithContent ensures the version reflects
// content changes so observability tooling can distinguish prompt
// revisions in logs/traces.
func TestEmbedStore_VersionChangesWithContent(t *testing.T) {
	tool := []byte(`{"type":"function","function":{"name":"x","description":"d","parameters":{}}}`)
	a := NewEmbed(fstest.MapFS{
		"x.md":        &fstest.MapFile{Data: []byte("v1")},
		"x.tool.json": &fstest.MapFile{Data: tool},
	})
	b := NewEmbed(fstest.MapFS{
		"x.md":        &fstest.MapFile{Data: []byte("v2")},
		"x.tool.json": &fstest.MapFile{Data: tool},
	})
	pa, err := a.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("Get a: %v", err)
	}
	pb, err := b.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("Get b: %v", err)
	}
	if pa.Version == pb.Version {
		t.Errorf("expected different versions for different bodies, both %q", pa.Version)
	}
}

// TestEmbedStore_MissingBodyReturnsNotFound is the contract for an absent
// prompt — callers (e.g. a future Langfuse-with-embed-fallback chain) use
// errors.Is(err, ErrNotFound) to decide whether to fall back.
func TestEmbedStore_MissingBodyReturnsNotFound(t *testing.T) {
	fsys := fstest.MapFS{}
	store := NewEmbed(fsys)
	_, err := store.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

// TestEmbedStore_MissingToolReturnsNotFound ensures the body-present /
// tool-absent split is treated as NotFound (rather than a partial success).
// A prompt without its tool schema is unusable, so the chain-fallback
// semantics should kick in.
func TestEmbedStore_MissingToolReturnsNotFound(t *testing.T) {
	fsys := fstest.MapFS{
		"halfdone.md": &fstest.MapFile{Data: []byte("body")},
	}
	store := NewEmbed(fsys)
	_, err := store.Get(context.Background(), "halfdone")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

// TestEmbedStore_InvalidToolJSONIsHardError ensures malformed tool schemas
// are NOT silently treated as not-found — they indicate a bug in the prompt
// repo and must surface so CI catches them. A fallback chain would re-raise
// this rather than swallowing it.
func TestEmbedStore_InvalidToolJSONIsHardError(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.md":        &fstest.MapFile{Data: []byte("body")},
		"bad.tool.json": &fstest.MapFile{Data: []byte("not json")},
	}
	store := NewEmbed(fsys)
	_, err := store.Get(context.Background(), "bad")
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid JSON must not be reported as NotFound, got %v", err)
	}
}

// TestEmbedStore_RejectsPathTraversal guards against accidental directory
// access if a caller passes an untrusted name. Prompt names are an
// internal contract but defensive validation is cheap.
func TestEmbedStore_RejectsPathTraversal(t *testing.T) {
	store := NewEmbed(fstest.MapFS{})
	_, err := store.Get(context.Background(), "../etc/passwd")
	if err == nil {
		t.Fatal("expected error for traversal name, got nil")
	}
}

// TestPrompt_RenderSubstitutesVars covers the template path: a Body with
// {{.Field}} placeholders renders against a data struct.
func TestPrompt_RenderSubstitutesVars(t *testing.T) {
	p := Prompt{Name: "t", Body: "hi {{.Name}}, you have {{.N}} items"}
	got, err := p.Render(struct {
		Name string
		N    int
	}{"alice", 3})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "hi alice, you have 3 items"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestPrompt_RenderRangeAndIfMatchesGoldenLayout verifies the {{- range}}
// and {{if}}{{end}} idioms produce the exact whitespace layout the
// extract/timeline prompts depend on. If this test breaks, the prompt
// .md files were edited in a way that changes whitespace semantics.
func TestPrompt_RenderRangeAndIfMatchesGoldenLayout(t *testing.T) {
	body := "header\n{{if .Show}}line: {{.Show}}\n{{end}}list:\n{{- range .Items}}\n  - {{.}}\n{{- end}}\n"
	p := Prompt{Name: "t", Body: body}
	got, err := p.Render(struct {
		Show  string
		Items []string
	}{"yes", []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "header\nline: yes\nlist:\n  - a\n  - b\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestPrompt_RenderMissingKeyIsError ensures a typo in the template
// fails loudly instead of rendering "<no value>" into a production
// LLM call.
func TestPrompt_RenderMissingKeyIsError(t *testing.T) {
	p := Prompt{Name: "t", Body: "hello {{.Missing}}"}
	_, err := p.Render(struct{ Other string }{"x"})
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
}

// TestPrompt_ValidateCatchesMalformedTemplate proves the startup-time
// validation surfaces a template syntax error so callers can panic
// before accepting traffic. The stray `{{` here is the kind of typo an
// editor might introduce when an author edits the embedded .md file by
// hand.
func TestPrompt_ValidateCatchesMalformedTemplate(t *testing.T) {
	p := Prompt{Name: "bad", Body: "hello {{.Name"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected Validate to reject malformed template, got nil")
	}
}

// TestPrompt_ValidateAcceptsWellFormedTemplate is the happy-path
// complement — confirms Validate does not over-reject on legitimate
// template constructs (range/if with whitespace trimming).
func TestPrompt_ValidateAcceptsWellFormedTemplate(t *testing.T) {
	body := "hi {{.Name}}{{if .Show}}: {{.Show}}{{end}}\n{{- range .Items}}\n- {{.}}\n{{- end}}"
	p := Prompt{Name: "ok", Body: body}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate rejected a well-formed template: %v", err)
	}
}
