// Package runtime is a thin client to octo-server's /v1/internal/bot-tasks
// endpoint. It dispatches "bot was assigned to a matter — go do it" tasks
// to whichever daemon hosts the bot's openclaw agent.
//
// PoC: best-effort fire-and-forget. A failed dispatch is logged but does
// not roll back the matter mutation; the v1.1 cutover will replace this with
// an outbox + retry pattern (see octo-daemon-cli/plan.md §4).
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Client posts bot tasks to octo-server. ServerBaseURL points at the octo-
// server install (e.g. http://127.0.0.1:8090); InternalToken is the shared
// X-Internal-Token. SelfBaseURL is the octo-matter base URL the server will
// call back to write timeline replies (e.g. http://host.docker.internal:8080).
type Client struct {
	ServerBaseURL string
	InternalToken string
	SelfBaseURL   string

	HTTP *http.Client
}

// IsBotUID is the PoC heuristic for "is this assignee a bot". Mints from
// botfather suffix the user_id with "_bot" (see octo-server/modules/botfather/
// command.go::tryCreateBotCore). PoC accepts this purely lexical check; v1.1
// will resolve via octo-server /v1/user/info?uid=X { robot: true }.
func IsBotUID(uid string) bool {
	return strings.HasSuffix(uid, "_bot")
}

// NewClient builds a Client with sensible defaults. Returns nil + logs a
// warning if serverURL is empty (matter service still runs; bot dispatch
// silently disabled — useful when octo-server isn't wired in dev).
func NewClient(serverURL, internalToken, selfBaseURL string) *Client {
	if strings.TrimSpace(serverURL) == "" {
		log.Printf("[runtime] OCTO_SERVER_URL not set — bot-task dispatch disabled")
		return nil
	}
	if strings.TrimSpace(internalToken) == "" {
		log.Printf("[runtime] NOTIFY_INTERNAL_TOKEN not set — bot-task dispatch will be rejected by server")
	}
	return &Client{
		ServerBaseURL: strings.TrimRight(serverURL, "/"),
		InternalToken: internalToken,
		SelfBaseURL:   strings.TrimRight(selfBaseURL, "/"),
		HTTP:          &http.Client{Timeout: 10 * time.Second},
	}
}

// BotTaskReq mirrors octo-server's createBotTaskReq. SelfBaseURL is the
// matter-service callback URL the server will use to POST the bot's reply
// back to /api/v1/internal/matters/:id/timeline.
//
// Prompt is optional — when non-empty it overrides the server-side default
// (which composes prompt from Title + Description). Used by mention
// dispatch to feed the agent the full conversation history.
type BotTaskReq struct {
	MatterID      string `json:"matter_id"`
	MatterBaseURL string `json:"matter_base_url"`
	SpaceID       string `json:"space_id"`
	BotUID        string `json:"bot_uid"`
	RequesterUID  string `json:"requester_uid"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Prompt        string `json:"prompt,omitempty"`
}

// EnqueueBotTask POSTs to /v1/internal/bot-tasks. Returns the server-assigned
// id on success. Error wrapping is detailed enough for the matter side to log
// + carry on; bot dispatch is non-critical to matter persistence.
func (c *Client) EnqueueBotTask(ctx context.Context, req BotTaskReq) (int64, error) {
	if c == nil {
		return 0, errors.New("runtime client is nil")
	}
	req.MatterBaseURL = c.SelfBaseURL
	buf, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal: %w", err)
	}
	endpoint := c.ServerBaseURL + "/v1/internal/bot-tasks"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Internal-Token", c.InternalToken)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("POST %s: status %d body=%s", endpoint, resp.StatusCode, string(body))
	}
	// octo-server wraps responses in {status, data}; extract data.id if so.
	var env struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
		ID int64 `json:"id"` // also accept flat shape
	}
	if err := json.Unmarshal(body, &env); err != nil {
		// Non-fatal: server accepted the task, we just can't echo the id.
		return 0, nil
	}
	if env.Data.ID != 0 {
		return env.Data.ID, nil
	}
	return env.ID, nil
}
