package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/option"
)

func TestOpenAIOfficialClient_CallTool(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path: got %s, want /chat/completions", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := `{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"created":0,
			"model":"gpt-test",
			"choices":[{
				"index":0,
				"message":{
					"role":"assistant",
					"tool_calls":[{
						"id":"call_1",
						"type":"function",
						"function":{"name":"extract","arguments":"{\"ok\":true}"}
					}]
				},
				"finish_reason":"tool_calls"
			}]
		}`
		if _, err := w.Write([]byte(resp)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer srv.Close()

	c := newOpenAIOfficialWithOptions(srv.URL, "key", "gpt-test", option.WithMaxRetries(0))
	raw, err := c.CallTool(context.Background(), "sys", "usr", testTool(), WithTemperature(0.2), WithMaxTokens(128))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if raw != `{"ok":true}` {
		t.Fatalf("raw args: got %s", raw)
	}
	if got["model"] != "gpt-test" {
		t.Fatalf("model: got %v", got["model"])
	}
	if got["temperature"] != 0.2 {
		t.Fatalf("temperature: got %v", got["temperature"])
	}
	if got["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens: got %v", got["max_tokens"])
	}
	tools := got["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("tool type: got %v", tool["type"])
	}
	choice := got["tool_choice"].(map[string]any)
	fn := choice["function"].(map[string]any)
	if fn["name"] != "extract" {
		t.Fatalf("tool_choice function name: got %v", fn["name"])
	}
}

func TestOpenAIOfficialClient_EmptyToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := `{"id":"chatcmpl_1","object":"chat.completion","created":0,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"no tool"},"finish_reason":"stop"}]}`
		if _, err := w.Write([]byte(resp)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer srv.Close()

	c := newOpenAIOfficialWithOptions(srv.URL, "key", "gpt-test", option.WithMaxRetries(0))
	if _, err := c.CallTool(context.Background(), "sys", "usr", testTool()); err != ErrEmptyToolCall {
		t.Fatalf("err: got %v, want ErrEmptyToolCall", err)
	}
}

func testTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "extract",
			Description: "extract data",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
				"required":   []any{"ok"},
			},
		},
	}
}
