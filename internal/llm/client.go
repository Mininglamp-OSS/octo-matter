// Package llm is a thin, OpenAI-compatible chat completions client supporting
// function calling. It targets any gateway that implements the OpenAI
// /v1/chat/completions schema (api.example.com, OpenAI, etc.).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxLLMResponseBytes bounds how much of an LLM gateway's response body we
// will buffer. See the call site in callChat for the rationale.
const maxLLMResponseBytes = 2 << 20 // 2 MB

// Message is a single chat message in the request.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tool describes a callable function exposed to the model.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the OpenAI function-call definition.
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// ToolChoice forces the model to call a specific tool. Use ForceTool to build it.
type ToolChoice struct {
	Type     string               `json:"type"`
	Function ToolChoiceFunctionID `json:"function"`
}

type ToolChoiceFunctionID struct {
	Name string `json:"name"`
}

// ForceTool builds a tool_choice payload that forces `name` to be called.
func ForceTool(name string) *ToolChoice {
	return &ToolChoice{Type: "function", Function: ToolChoiceFunctionID{Name: name}}
}

// ChatRequest mirrors the OpenAI /v1/chat/completions request body.
type ChatRequest struct {
	Model      string      `json:"model"`
	Messages   []Message   `json:"messages"`
	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
}

// ChatResponse is the narrow subset of OpenAI's response we care about.
type ChatResponse struct {
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// ToolCall is a single function call emitted by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Client speaks the OpenAI chat-completions dialect.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// New returns a ready-to-use Client. baseURL should already include "/v1".
func New(baseURL, apiKey, model string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: timeout},
	}
}

// Model reports the model identifier the client was built with.
func (c *Client) Model() string { return c.model }

// ErrEmptyToolCall is returned when the model reply contains no function call
// (or the forced tool was not invoked).
//
// Service callers also wrap this sentinel when the model DID invoke the tool
// but the returned arguments were empty/unusable (blank title, blank content,
// etc.) — both conditions surface to the API as 422 LLM_EMPTY_EXTRACTION, so
// reusing the sentinel keeps handler dispatch single-branch. Treat it as
// "model produced no usable extraction" rather than the literal "no tool call."
var ErrEmptyToolCall = errors.New("llm: no tool call returned")

// ChatCompletion runs a chat-completions request against the configured backend.
func (c *Client) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.model
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("llm: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()
	// Cap the response we will buffer to defend against a misbehaving or
	// compromised LLM gateway returning a multi-GB body. 2 MB is well above
	// the largest legitimate completion this service expects.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: upstream status %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w", err)
	}
	return &out, nil
}

// CallTool performs a forced-function-call and returns the raw JSON arguments
// string emitted by the model for the tool named `toolName`.
func (c *Client) CallTool(ctx context.Context, systemPrompt, userPrompt string, tool Tool) (string, error) {
	resp, err := c.ChatCompletion(ctx, ChatRequest{
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Tools:      []Tool{tool},
		ToolChoice: ForceTool(tool.Function.Name),
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
		return "", ErrEmptyToolCall
	}
	for _, tc := range resp.Choices[0].Message.ToolCalls {
		if tc.Function.Name == tool.Function.Name {
			return tc.Function.Arguments, nil
		}
	}
	return "", ErrEmptyToolCall
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
