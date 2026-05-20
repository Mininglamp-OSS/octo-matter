package promptstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// LangfuseStore fetches prompt bodies from a Langfuse instance via its
// public REST API (GET /api/public/v2/prompts/:name?label=...). Tool
// schemas are intentionally NOT sourced from Langfuse — JSON Schema is
// part of the engineering contract and stays with the code via the
// injected toolSource (typically the same EmbedStore the application
// uses as a fallback).
//
// Responses are cached for cacheTTL keyed by (name, label) to keep the
// hot path off the network. The cache is bounded by call diversity, not
// size; current usage is two prompt names, so an unevictable map is fine.
//
// A LangfuseStore in isolation is not the recommended deployment — wrap
// it with FallbackStore{Primary: lf, Fallback: embed} so a Langfuse
// outage degrades to the embedded prompts rather than failing user
// requests.
type LangfuseStore struct {
	host       string
	publicKey  string
	secretKey  string
	label      string
	httpClient *http.Client
	cacheTTL   time.Duration
	toolSource Store

	mu    sync.Mutex
	cache map[string]langfuseCacheEntry
}

type langfuseCacheEntry struct {
	prompt  Prompt
	storeAt time.Time
}

// LangfuseOption configures a LangfuseStore at construction time.
type LangfuseOption func(*LangfuseStore)

// WithLangfuseLabel selects which Langfuse label (e.g. "production",
// "staging") to request. Defaults to "production".
func WithLangfuseLabel(label string) LangfuseOption {
	return func(s *LangfuseStore) { s.label = label }
}

// WithLangfuseTimeout sets the per-request HTTP timeout. Defaults to 5s.
// Ignored when WithLangfuseHTTPClient is also supplied.
func WithLangfuseTimeout(d time.Duration) LangfuseOption {
	return func(s *LangfuseStore) {
		if s.httpClient == nil || s.httpClient == http.DefaultClient {
			s.httpClient = &http.Client{Timeout: d}
		}
	}
}

// WithLangfuseHTTPClient injects a custom *http.Client (useful for tests
// or for plugging in a shared transport with metrics).
func WithLangfuseHTTPClient(c *http.Client) LangfuseOption {
	return func(s *LangfuseStore) { s.httpClient = c }
}

// WithLangfuseCacheTTL overrides the response-cache lifetime. Defaults to
// 60 seconds — short enough that Langfuse-side prompt edits propagate
// without a process restart, long enough to avoid hammering the API on
// every LLM call.
func WithLangfuseCacheTTL(d time.Duration) LangfuseOption {
	return func(s *LangfuseStore) { s.cacheTTL = d }
}

// WithLangfuseToolSource injects the Store that supplies tool schemas for
// prompts fetched from Langfuse. This is required; constructing a
// LangfuseStore without one will cause every Get to error.
func WithLangfuseToolSource(src Store) LangfuseOption {
	return func(s *LangfuseStore) { s.toolSource = src }
}

// NewLangfuse builds a LangfuseStore targeting host with HTTP Basic auth
// derived from publicKey:secretKey. Callers should pass at least
// WithLangfuseToolSource — without it, Get will fail.
func NewLangfuse(host, publicKey, secretKey string, opts ...LangfuseOption) *LangfuseStore {
	s := &LangfuseStore{
		host:       host,
		publicKey:  publicKey,
		secretKey:  secretKey,
		label:      "production",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		cacheTTL:   60 * time.Second,
		cache:      make(map[string]langfuseCacheEntry),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// langfuseResponse models the subset of GET /api/public/v2/prompts/:name
// we depend on. "prompt" is decoded as json.RawMessage because the field
// is a string for text-type prompts and an array for chat-type — we
// reject chat in Step 2 and parse only text.
type langfuseResponse struct {
	Name    string          `json:"name"`
	Version int             `json:"version"`
	Type    string          `json:"type"`
	Prompt  json.RawMessage `json:"prompt"`
}

// Get fetches the prompt body from Langfuse and combines it with the
// in-code tool schema. Cached results within cacheTTL skip the network.
func (s *LangfuseStore) Get(ctx context.Context, name string) (Prompt, error) {
	if s.toolSource == nil {
		return Prompt{}, fmt.Errorf("langfuse: tool source not configured for %q", name)
	}

	key := s.label + "/" + name
	if p, ok := s.cachedPrompt(key); ok {
		return p, nil
	}

	body, version, err := s.fetch(ctx, name)
	if err != nil {
		return Prompt{}, err
	}
	tool, err := s.toolSource.Get(ctx, name)
	if err != nil {
		return Prompt{}, fmt.Errorf("langfuse: tool source for %q: %w", name, err)
	}
	p := Prompt{
		Name:    name,
		Version: "langfuse:" + strconv.Itoa(version),
		Body:    body,
		Tool:    tool.Tool,
	}
	s.storePrompt(key, p)
	return p, nil
}

func (s *LangfuseStore) cachedPrompt(key string) (Prompt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok {
		return Prompt{}, false
	}
	if time.Since(e.storeAt) > s.cacheTTL {
		delete(s.cache, key)
		return Prompt{}, false
	}
	return e.prompt, true
}

func (s *LangfuseStore) storePrompt(key string, p Prompt) {
	s.mu.Lock()
	s.cache[key] = langfuseCacheEntry{prompt: p, storeAt: time.Now()}
	s.mu.Unlock()
}

// fetch performs the HTTP GET and returns (body, version) on success.
// 404 is mapped to ErrNotFound so FallbackStore can distinguish "prompt
// missing" from "Langfuse broken".
func (s *LangfuseStore) fetch(ctx context.Context, name string) (string, int, error) {
	u, err := url.Parse(s.host)
	if err != nil {
		return "", 0, fmt.Errorf("langfuse: parse host: %w", err)
	}
	u.Path = "/api/public/v2/prompts/" + url.PathEscape(name)
	q := u.Query()
	q.Set("label", s.label)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, fmt.Errorf("langfuse: new request: %w", err)
	}
	req.SetBasicAuth(s.publicKey, s.secretKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("langfuse: GET %s: %w", name, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		return "", 0, fmt.Errorf("langfuse: prompt %q: %w", name, ErrNotFound)
	default:
		return "", 0, fmt.Errorf("langfuse: GET %s: status %d", name, resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("langfuse: read body for %q: %w", name, err)
	}
	var lr langfuseResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return "", 0, fmt.Errorf("langfuse: decode response for %q: %w", name, err)
	}
	if lr.Type != "" && lr.Type != "text" {
		return "", 0, fmt.Errorf("langfuse: prompt %q has unsupported type %q (only text is supported)", name, lr.Type)
	}
	var body string
	if err := json.Unmarshal(lr.Prompt, &body); err != nil {
		return "", 0, fmt.Errorf("langfuse: prompt %q body is not a string: %w", name, err)
	}
	return body, lr.Version, nil
}

var _ Store = (*LangfuseStore)(nil)
