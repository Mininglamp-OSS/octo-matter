package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const defaultAnthropicMaxTokens int64 = 4096

// AnthropicOfficialClient calls Claude Messages through the official SDK.
// It normalizes Anthropic tool_use blocks back to the same JSON-arguments
// string returned by the compat client.
type AnthropicOfficialClient struct {
	client anthropic.Client
	model  string
}

// NewAnthropicOfficial returns an Anthropic official-SDK-backed tool caller.
// The Anthropic SDK appends /v1/messages itself; if baseURL already ends in
// /v1, it is trimmed to preserve the repository's existing LLM_API_URL habit.
func NewAnthropicOfficial(baseURL, apiKey, model string, timeout time.Duration) *AnthropicOfficialClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return newAnthropicOfficialWithOptions(baseURL, apiKey, model,
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
	)
}

func newAnthropicOfficialWithOptions(baseURL, apiKey, model string, opts ...option.RequestOption) *AnthropicOfficialClient {
	clientOpts := []option.RequestOption{
		option.WithBaseURL(normalizeAnthropicBaseURL(baseURL)),
		option.WithAPIKey(apiKey),
	}
	clientOpts = append(clientOpts, opts...)
	return &AnthropicOfficialClient{
		client: anthropic.NewClient(clientOpts...),
		model:  model,
	}
}

// Model reports the model identifier the client was built with.
func (c *AnthropicOfficialClient) Model() string { return c.model }

// CallTool performs a forced Anthropic tool call and returns the selected
// tool_use input as a JSON string.
func (c *AnthropicOfficialClient) CallTool(ctx context.Context, systemPrompt, userPrompt string, tool Tool, opts ...CallOption) (string, error) {
	schema, err := anthropicInputSchema(tool.Function.Parameters)
	if err != nil {
		return "", err
	}
	req := ChatRequest{
		Tools:      []Tool{tool},
		ToolChoice: ForceTool(tool.Function.Name),
	}
	for _, opt := range opts {
		opt(&req)
	}

	system := anthropic.TextBlockParam{Text: systemPrompt}
	if req.CacheSystemPrompt {
		system.CacheControl = anthropic.CacheControlEphemeralParam{
			TTL: anthropic.CacheControlEphemeralTTLTTL1h,
		}
	}
	maxTokens := defaultAnthropicMaxTokens
	if req.MaxTokens != nil {
		maxTokens = int64(*req.MaxTokens)
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{system},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt))},
		Tools: []anthropic.ToolUnionParam{
			{
				OfTool: &anthropic.ToolParam{
					Name:        tool.Function.Name,
					Description: anthropic.String(tool.Function.Description),
					InputSchema: schema,
				},
			},
		},
		ToolChoice: anthropic.ToolChoiceParamOfTool(tool.Function.Name),
	}
	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	}

	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("llm anthropic official: %w", err)
	}
	for _, block := range resp.Content {
		if block.Type != "tool_use" || block.Name != tool.Function.Name {
			continue
		}
		if len(block.Input) == 0 {
			return "", ErrEmptyToolCall
		}
		return string(block.Input), nil
	}
	return "", ErrEmptyToolCall
}

func normalizeAnthropicBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	return strings.TrimSuffix(baseURL, "/v1")
}

func anthropicInputSchema(parameters any) (anthropic.ToolInputSchemaParam, error) {
	params, err := functionParametersMap(parameters)
	if err != nil {
		return anthropic.ToolInputSchemaParam{}, err
	}
	var schema anthropic.ToolInputSchemaParam
	raw, err := json.Marshal(params)
	if err != nil {
		return anthropic.ToolInputSchemaParam{}, fmt.Errorf("llm: marshal anthropic tool schema: %w", err)
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return anthropic.ToolInputSchemaParam{}, fmt.Errorf("llm: decode anthropic tool schema: %w", err)
	}
	return schema, nil
}
