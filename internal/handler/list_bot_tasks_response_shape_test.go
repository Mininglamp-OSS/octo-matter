package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

// ListBotTasksForDaemon empty-owned branches return JSON with the
// `tasks` field. The handler is exercised via a minimal gin engine
// that mounts the route + pre-populates the verified-ctx keys.
//
// We exercise both early-return branches in a single test that runs
// two requests against the same handler:
//   - `?bot_uid=foreign_uid`         → single-bot path, owned filter empties uids
//   - `?bot_uids=foreign,other`      → multi-bot  path, intersectStrings empties uids
// Both should respond with HTTP 200 + body `{"tasks": []}`.
func TestListBotTasksForDaemon_EmptyOwned_ReturnsTasksKey(t *testing.T) {
	// botTaskRepo non-nil so the early "NOT_AVAILABLE" 503 doesn't
	// trip. The empty-owned branches don't call any method on the
	// repo, so a zero-value struct is safe.
	h := &InternalHandler{botTaskRepo: &repository.BotTaskRepo{}}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/internal/bot-tasks", func(c *gin.Context) {
		// Verified ctx as AuthMiddleware would inject: caller has zero
		// owned bots in this space, so the ownership filter empties
		// every requested bot_uid.
		c.Set("uid", "user_x")
		c.Set("space_id", "space-A")
		c.Set("auth_kind", "apikey")
		c.Set("owned_bots_by_space", map[string][]string{"space-A": {}})
		h.ListBotTasksForDaemon(c)
	})

	cases := []struct {
		name  string
		query string
	}{
		{"single-bot path", "bot_uid=foreign_bot"},
		{"multi-bot path", "bot_uids=foreign1,foreign2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/bot-tasks?"+tc.query, nil)
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
			}
			// Decode into the documented shape — fails if a bare `[]`
			// is returned instead of `{"tasks":[...]}` (the v3.3.1
			// regression).
			var resp struct {
				Tasks *[]struct{} `json:"tasks"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("body must decode as {\"tasks\":...} — v3.3.2 #3 wire contract; got body=%s err=%v",
					w.Body.String(), err)
			}
			if resp.Tasks == nil {
				t.Errorf("body must contain `tasks` field (v3.3.2 #3); body=%s", w.Body.String())
			}
		})
	}
}
