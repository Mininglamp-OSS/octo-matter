package service

import (
	"context"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm"
)

// LLMToolCaller is the function-calling surface consumed by services.
// Provider-specific clients live in internal/llm and are selected by cmd/main.go.
type LLMToolCaller interface {
	CallTool(ctx context.Context, systemPrompt, userPrompt string, tool llm.Tool, opts ...llm.CallOption) (string, error)
	Model() string
}
