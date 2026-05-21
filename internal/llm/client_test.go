package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCallTool_AppliesOptions asserts that WithTemperature / WithMaxTokens
// reach the wire as the corresponding ChatRequest fields, and that omitting
// them leaves the gateway to apply defaults (no temperature/max_tokens keys
// at all). The gateway-default branch matters because some providers reject
// "temperature: 0" with a 400.
func TestCallTool_AppliesOptions(t *testing.T) {
	type wire struct {
		Temperature *float64 `json:"temperature"`
		MaxTokens   *int     `json:"max_tokens"`
	}

	cases := []struct {
		name       string
		opts       []CallOption
		wantTemp   *float64
		wantTokens *int
	}{
		{
			name:       "no opts → fields omitted",
			opts:       nil,
			wantTemp:   nil,
			wantTokens: nil,
		},
		{
			name:       "temperature only",
			opts:       []CallOption{WithTemperature(0.2)},
			wantTemp:   ptrF(0.2),
			wantTokens: nil,
		},
		{
			// temperature=0 is the load-bearing case: it must serialize as
			// a non-nil pointer so it appears on the wire, not be conflated
			// with "unset" (which would let the gateway default win).
			name:       "both, including temperature=0",
			opts:       []CallOption{WithTemperature(0.0), WithMaxTokens(512)},
			wantTemp:   ptrF(0.0),
			wantTokens: ptrI(512),
		},
		{
			name:       "later option overrides earlier",
			opts:       []CallOption{WithTemperature(0.9), WithTemperature(0.1)},
			wantTemp:   ptrF(0.1),
			wantTokens: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var got wire
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read request: %v", err)
				}
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("unmarshal request: %v", err)
				}
				// Minimal valid tool-call response.
				w.Header().Set("Content-Type", "application/json")
				resp := `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"t","arguments":"{}"}}]}}]}`
				if _, err := w.Write([]byte(resp)); err != nil {
					t.Fatalf("write response: %v", err)
				}
			}))
			defer srv.Close()

			c := New(srv.URL, "k", "m", 5*time.Second)
			tool := Tool{Type: "function", Function: ToolFunction{Name: "t", Parameters: map[string]any{}}}
			if _, err := c.CallTool(context.Background(), "sys", "usr", tool, tc.opts...); err != nil {
				t.Fatalf("CallTool: %v", err)
			}

			if !eqF(got.Temperature, tc.wantTemp) {
				t.Errorf("temperature: got %v, want %v", strFloat(got.Temperature), strFloat(tc.wantTemp))
			}
			if !eqI(got.MaxTokens, tc.wantTokens) {
				t.Errorf("max_tokens: got %v, want %v", strInt(got.MaxTokens), strInt(tc.wantTokens))
			}
		})
	}
}

// TestCallTool_OmittedFieldsAbsentFromJSON locks down the "omitempty" contract:
// a default ChatRequest must not serialize temperature/max_tokens keys at all.
// Some upstream gateways (litellm, vllm) reject extra null keys, so the
// pointer + omitempty combo is load-bearing.
func TestCallTool_OmittedFieldsAbsentFromJSON(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		raw = string(b)
		w.Header().Set("Content-Type", "application/json")
		resp := `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"t","arguments":"{}"}}]}}]}`
		if _, err := w.Write([]byte(resp)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "m", 5*time.Second)
	tool := Tool{Type: "function", Function: ToolFunction{Name: "t", Parameters: map[string]any{}}}
	if _, err := c.CallTool(context.Background(), "sys", "usr", tool); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if strings.Contains(raw, `"temperature"`) {
		t.Errorf("unexpected temperature key in default request body: %s", raw)
	}
	if strings.Contains(raw, `"max_tokens"`) {
		t.Errorf("unexpected max_tokens key in default request body: %s", raw)
	}
}

func ptrF(f float64) *float64 { return &f }
func ptrI(i int) *int         { return &i }

func eqF(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
func eqI(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func strFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
func strInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
