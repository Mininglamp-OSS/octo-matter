package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/gin-gonic/gin"
)

// TestI18nEndToEnd exercises the full request path: EarlyMiddleware negotiates
// the language, the handler returns an apperr, and respondErr localizes the
// message + sets Content-Language — proving the wiring, not just the unit.
func TestI18nEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(i18n.EarlyMiddleware())
	r.GET("/matter", func(c *gin.Context) { respondErr(c, apperr.MatterNotFound()) })
	r.GET("/bind", func(c *gin.Context) {
		failKey(c, http.StatusBadRequest, "VALIDATION_ERROR", i18n.KeyContentTooLong, nil)
	})

	type tc struct {
		name, path, acceptLang, wantMsg, wantContentLang string
	}
	cases := []tc{
		{"en matter", "/matter", "en-US", "matter not found", "en-US"},
		{"zh matter", "/matter", "zh-CN", "任务未找到", "zh-CN"},
		{"default matter (no header)", "/matter", "", "任务未找到", "zh-CN"},
		{"query overrides accept", "/matter?lang=en-US", "zh-CN", "matter not found", "en-US"},
		{"en validation", "/bind", "en-US", "content too long", "en-US"},
		{"zh validation", "/bind", "zh-CN", "内容过长", "zh-CN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			if c.acceptLang != "" {
				req.Header.Set("Accept-Language", c.acceptLang)
			}
			r.ServeHTTP(w, req)

			if got := w.Header().Get("Content-Language"); got != c.wantContentLang {
				t.Errorf("Content-Language = %q, want %q", got, c.wantContentLang)
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", w.Body.String(), err)
			}
			if body.Error.Message != c.wantMsg {
				t.Errorf("message = %q, want %q", body.Error.Message, c.wantMsg)
			}
		})
	}
}
