package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestI18nBindErrorNoLeak verifies that a JSON bind failure returns a localized
// generic message and does NOT echo the raw (English, field-naming) validator
// error in the response body.
func TestI18nBindErrorNoLeak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(i18n.EarlyMiddleware())
	r.POST("/create", func(c *gin.Context) {
		var req struct {
			Title string `json:"title" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			bindJSONErr(c, err)
			return
		}
		ok(c, gin.H{"title": req.Title})
	})

	for _, tc := range []struct{ lang, want string }{
		{"en-US", "invalid request body"},
		{"zh-CN", "请求体无效"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", tc.lang)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("[%s] status = %d, want 400", tc.lang, w.Code)
		}
		var body map[string]map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("[%s] decode: %v", tc.lang, err)
		}
		if got := body["error"]["message"]; got != tc.want {
			t.Errorf("[%s] message = %v, want %q", tc.lang, got, tc.want)
		}
		// The leak fix: no details echoed, and no raw validator text anywhere.
		if _, ok := body["error"]["details"]; ok {
			t.Errorf("[%s] details should be absent, got %v", tc.lang, body["error"]["details"])
		}
		if strings.Contains(w.Body.String(), "Field") || strings.Contains(w.Body.String(), "validation") {
			t.Errorf("[%s] body leaks raw validator text: %s", tc.lang, w.Body.String())
		}
	}
}
