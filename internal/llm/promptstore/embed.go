package promptstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
)

// EmbedStore serves prompts from an in-tree filesystem (typically a Go
// embed.FS). For each prompt name it expects two files:
//
//	{name}.md         — text/template source for the system prompt
//	{name}.tool.json  — JSON-encoded llm.Tool function-call schema
//
// Both files must be present or Get returns ErrNotFound.
type EmbedStore struct {
	fsys fs.FS
}

// NewEmbed wraps fsys (typically an embed.FS) as a Store.
func NewEmbed(fsys fs.FS) *EmbedStore {
	return &EmbedStore{fsys: fsys}
}

// Get loads the prompt body and tool schema for name. The returned Version
// is a content hash so different prompt contents always surface as
// different versions in logs.
func (s *EmbedStore) Get(_ context.Context, name string) (Prompt, error) {
	if strings.ContainsAny(name, "/\\") {
		return Prompt{}, fmt.Errorf("promptstore: invalid prompt name %q", name)
	}
	body, err := fs.ReadFile(s.fsys, name+".md")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Prompt{}, fmt.Errorf("%s body: %w", name, ErrNotFound)
		}
		return Prompt{}, fmt.Errorf("read prompt body %s: %w", name, err)
	}
	toolRaw, err := fs.ReadFile(s.fsys, name+".tool.json")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Prompt{}, fmt.Errorf("%s tool schema: %w", name, ErrNotFound)
		}
		return Prompt{}, fmt.Errorf("read prompt tool %s: %w", name, err)
	}
	var tool llm.Tool
	if err := json.Unmarshal(toolRaw, &tool); err != nil {
		return Prompt{}, fmt.Errorf("decode tool schema %s: %w", name, err)
	}
	if tool.Function.Name == "" {
		return Prompt{}, fmt.Errorf("tool schema %s: function.name empty", name)
	}
	sum := sha256.Sum256(append(append([]byte{}, body...), toolRaw...))
	return Prompt{
		Name:    name,
		Version: "embed:" + hex.EncodeToString(sum[:8]),
		Body:    string(body),
		Tool:    tool,
	}, nil
}
