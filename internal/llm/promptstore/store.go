// Package promptstore loads named system-prompt templates and their paired
// function-call tool schemas. The Store interface lets callers swap the
// backing source (embedded files, Langfuse, etc.) without touching service
// code. A Prompt's Body is a text/template source — call Render to
// substitute variables.
package promptstore

import (
	"bytes"
	"context"
	"errors"
	"text/template"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
)

// ErrNotFound is returned by Store.Get when no prompt is registered under
// the requested name.
var ErrNotFound = errors.New("promptstore: prompt not found")

// Prompt is a named system-prompt template paired with its tool schema.
// Version is opaque to callers and is used only for observability (e.g.
// logging which template produced a given LLM call). For embed-backed
// stores it is derived from a content hash; for remote stores (Langfuse)
// it is the upstream version label.
type Prompt struct {
	Name    string
	Version string
	Body    string
	Tool    llm.Tool
}

// Render executes Body as a text/template against data and returns the
// resulting string. Missing keys are treated as errors so that a typo in
// the template surfaces immediately rather than silently rendering "<no
// value>".
func (p Prompt) Render(data any) (string, error) {
	t, err := template.New(p.Name).Option("missingkey=error").Parse(p.Body)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Store retrieves a Prompt by name. Implementations must return ErrNotFound
// (wrapped) when the name is unknown.
type Store interface {
	Get(ctx context.Context, name string) (Prompt, error)
}
