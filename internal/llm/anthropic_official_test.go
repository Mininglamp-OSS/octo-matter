package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

func TestAnthropicOfficialClient_CallTool(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path: got %s, want /v1/messages", r.URL.Path)
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
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{
				"type":"tool_use",
				"id":"toolu_1",
				"name":"extract",
				"input":{"ok":true}
			}],
			"stop_reason":"tool_use",
			"stop_sequence":null,
			"usage":{"input_tokens":1,"output_tokens":1}
		}`
		if _, err := w.Write([]byte(resp)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer srv.Close()

	c := newAnthropicOfficialWithOptions(srv.URL+"/v1", "key", "claude-test", option.WithMaxRetries(0))
	raw, err := c.CallTool(context.Background(), "sys", "usr", testTool(), WithTemperature(0.2), WithMaxTokens(128), WithSystemPromptCache())
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if raw != `{"ok":true}` {
		t.Fatalf("raw args: got %s", raw)
	}
	if got["model"] != "claude-test" {
		t.Fatalf("model: got %v", got["model"])
	}
	if got["temperature"] != 0.2 {
		t.Fatalf("temperature: got %v", got["temperature"])
	}
	if got["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens: got %v", got["max_tokens"])
	}
	system := got["system"].([]any)[0].(map[string]any)
	if system["text"] != "sys" {
		t.Fatalf("system text: got %v", system["text"])
	}
	cacheControl, ok := system["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("expected system cache_control, got %#v", system["cache_control"])
	}
	if cacheControl["type"] != "ephemeral" {
		t.Fatalf("cache_control type: got %v", cacheControl["type"])
	}
	choice := got["tool_choice"].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != "extract" {
		t.Fatalf("tool_choice: got %#v", choice)
	}
}

func TestAnthropicOfficialClient_EmptyToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"no tool"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`
		if _, err := w.Write([]byte(resp)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer srv.Close()

	c := newAnthropicOfficialWithOptions(srv.URL, "key", "claude-test", option.WithMaxRetries(0))
	if _, err := c.CallTool(context.Background(), "sys", "usr", testTool()); err != ErrEmptyToolCall {
		t.Fatalf("err: got %v, want ErrEmptyToolCall", err)
	}
}
