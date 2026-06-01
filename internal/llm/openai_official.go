package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAIOfficialClient calls OpenAI Chat Completions through the official SDK.
// It preserves Client.CallTool's forced-tool-call contract for service callers.
type OpenAIOfficialClient struct {
	client openai.Client
	model  string
}

// NewOpenAIOfficial returns an OpenAI official-SDK-backed tool caller.
// baseURL should include the API version path, e.g. https://api.openai.com/v1.
func NewOpenAIOfficial(baseURL, apiKey, model string, timeout time.Duration) *OpenAIOfficialClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return newOpenAIOfficialWithOptions(baseURL, apiKey, model,
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
	)
}

func newOpenAIOfficialWithOptions(baseURL, apiKey, model string, opts ...option.RequestOption) *OpenAIOfficialClient {
	clientOpts := []option.RequestOption{
		option.WithBaseURL(strings.TrimRight(baseURL, "/")),
		option.WithAPIKey(apiKey),
	}
	clientOpts = append(clientOpts, opts...)
	return &OpenAIOfficialClient{
		client: openai.NewClient(clientOpts...),
		model:  model,
	}
}

// Model reports the model identifier the client was built with.
func (c *OpenAIOfficialClient) Model() string { return c.model }

// CallTool performs a forced tool call and returns the selected tool's raw JSON
// arguments, matching the compat client's contract.
func (c *OpenAIOfficialClient) CallTool(ctx context.Context, systemPrompt, userPrompt string, tool Tool, opts ...CallOption) (string, error) {
	paramsMap, err := functionParametersMap(tool.Function.Parameters)
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

	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(c.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{OfString: openai.String(systemPrompt)},
				},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String(userPrompt)},
				},
			},
		},
		Tools: []openai.ChatCompletionToolParam{
			{
				Function: openai.FunctionDefinitionParam{
					Name:        tool.Function.Name,
					Description: openai.String(tool.Function.Description),
					Parameters:  openai.FunctionParameters(paramsMap),
				},
			},
		},
		ToolChoice: openai.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(
			openai.ChatCompletionNamedToolChoiceFunctionParam{Name: tool.Function.Name},
		),
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}
	if req.MaxTokens != nil {
		params.MaxTokens = openai.Int(int64(*req.MaxTokens))
	}

	resp, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("llm openai official: %w", err)
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

func functionParametersMap(parameters any) (map[string]any, error) {
	if parameters == nil {
		return map[string]any{}, nil
	}
	if params, ok := parameters.(map[string]any); ok {
		return params, nil
	}
	raw, err := json.Marshal(parameters)
	if err != nil {
		return nil, fmt.Errorf("llm: marshal tool parameters: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("llm: decode tool parameters: %w", err)
	}
	return out, nil
}
