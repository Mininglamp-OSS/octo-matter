package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMatchSupported(t *testing.T) {
	cases := map[string]string{
		"zh-CN":          LangZhCN,
		"zh":             LangZhCN,
		"zh-Hans":        LangZhCN,
		"en-US":          LangEnUS,
		"en":             LangEnUS,
		"EN-us":          LangEnUS,
		"zh-CN,zh;q=0.9": LangZhCN,
		"fr-FR":          "",
		"":               "",
	}
	for in, want := range cases {
		if got := MatchSupported(in); got != want {
			t.Errorf("MatchSupported(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNegotiatePriority(t *testing.T) {
	// X-Octo-Lang outranks every other request signal.
	r := httptest.NewRequest(http.MethodGet, "/?lang=en-US", nil)
	r.Header.Set(HeaderOctoLang, "zh-CN")
	r.Header.Set("Accept-Language", "en-US")
	r.AddCookie(&http.Cookie{Name: CookieLanguage, Value: "en-US"})
	if d := Negotiate(r); d.Language != LangZhCN || d.Source != SourceTrustedHeader {
		t.Fatalf("trusted header should win: got %+v", d)
	}

	// ?lang beats cookie + Accept-Language.
	r = httptest.NewRequest(http.MethodGet, "/?lang=en-US", nil)
	r.Header.Set("Accept-Language", "zh-CN")
	r.AddCookie(&http.Cookie{Name: CookieLanguage, Value: "zh-CN"})
	if d := Negotiate(r); d.Language != LangEnUS || d.Source != SourceQuery {
		t.Fatalf("query should win over cookie/accept: got %+v", d)
	}

	// cookie beats Accept-Language.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "zh-CN")
	r.AddCookie(&http.Cookie{Name: CookieLanguage, Value: "en-US"})
	if d := Negotiate(r); d.Language != LangEnUS || d.Source != SourceCookie {
		t.Fatalf("cookie should win over accept: got %+v", d)
	}

	// Accept-Language when nothing else is set.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "en-US")
	if d := Negotiate(r); d.Language != LangEnUS || d.Source != SourceAccept {
		t.Fatalf("accept-language should be used: got %+v", d)
	}

	// Default fallback (zh-CN) when no signal.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	if d := Negotiate(r); d.Language != LangZhCN || d.Source != SourceDefault {
		t.Fatalf("default should be zh-CN: got %+v", d)
	}
}

func TestLocalizeBothLanguages(t *testing.T) {
	if got := Localize(LangEnUS, KeyMatterNotFound, nil); got != "matter not found" {
		t.Errorf("en-US matter not found = %q", got)
	}
	if got := Localize(LangZhCN, KeyMatterNotFound, nil); got != "任务未找到" {
		t.Errorf("zh-CN matter not found = %q", got)
	}
	// Templated key.
	en := Localize(LangEnUS, KeyMsgsLimit, map[string]any{"Limit": 200})
	if en != "msgs exceeds limit of 200" {
		t.Errorf("en-US templated = %q", en)
	}
	zh := Localize(LangZhCN, KeyMsgsLimit, map[string]any{"Limit": 200})
	if zh != "msgs 超过上限 200" {
		t.Errorf("zh-CN templated = %q", zh)
	}
}

func TestLocalizeUnknownKeyFallsBackToKey(t *testing.T) {
	const missing = "err.does.not.exist"
	if got := Localize(LangZhCN, missing, nil); got != missing {
		t.Errorf("unknown key should fall back to the key, got %q", got)
	}
}

// allKeys lists every catalog key referenced from Go so the test can assert the
// catalog has no missing translations in either language.
var allKeys = []string{
	KeyNotFound, KeyForbidden, KeyInternal, KeyPayloadTooLarge,
	KeyAuthMissingToken, KeyAuthUnavailable, KeyAuthInvalidToken, KeyAuthInvalidBotToken, KeyAuthParseFailed,
	KeySpaceMissingHeader, KeySpaceForbidden, KeySpaceUnavailable,
	KeyInvalidID, KeyInvalidEntryID, KeyInvalidCursor, KeyInvalidRequest, KeyMatterIDMismatch,
	KeyContentRequired, KeyContentTooLong, KeyTooManyAttachments, KeyFileURLRequired, KeyAttachmentTooLarge,
	KeyParticipantUIDReq, KeyChannelIDRequired, KeyChannelTypeInvalid, KeyCreatorUIDRequired,
	KeyMsgsRequired, KeyMsgsLimit, KeyMessageIDLimit, KeyDeadlineFormat, KeyRemindAtFormat,
	KeyStatusInvalid, KeyAssigneeUIDRequired, KeyCallerIdentityReq, KeyTransitionArchived,
	KeyMatterAccess, KeyMatterView, KeyParticipantNotAuthorized, KeyCreatorUIDNotAuthorized,
	KeyBotLinkChannel, KeyBotLinkExisting, KeyNotChannelMember, KeyOnlyCreatorArchive, KeyOnlyCreatorOrAssigneeStat,
	KeyMatterNotFound, KeyAssigneeNotFound,
	KeyUpstream, KeyChannelMembership, KeyRateLimited, KeyRateLimitCooldown, KeyDuplicateAssignee,
	KeyLLMEmptyExtraction, KeyLLMUpstream,
	KeyNotifyMatterCreated, KeyNotifyStatusChanged, KeyNotifyAssigneeAdded, KeyNotifyTimelineEntryAdded,
	KeyNotifyActionDone, KeyNotifyActionArchived, KeyNotifyActionOpen, KeyNotifyActionUpdated,
}

func TestCatalogCompleteness(t *testing.T) {
	// Params that satisfy the templated keys so rendering does not error.
	params := map[string]any{"Title": "T", "Actor": "A", "Action": "X", "Limit": 1, "Index": 0, "Cooldown": "10s"}
	for _, lang := range []string{LangEnUS, LangZhCN} {
		for _, key := range allKeys {
			got := Localize(lang, key, params)
			if got == key {
				t.Errorf("missing %s translation for key %q (fell back to the key)", lang, key)
			}
		}
	}
}

func TestPromoteUserLanguageRespectsPriority(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// A SourceUser preference promotes over an Accept-Language decision.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	setDecision(c, Decision{Language: LangEnUS, Source: SourceAccept})
	PromoteUserLanguage(c, "zh-CN")
	if d := FromGin(c); d.Language != LangZhCN || d.Source != SourceUser {
		t.Errorf("user pref should promote over accept: got %+v", d)
	}

	// But an explicit trusted-header choice is NOT overridden by user pref.
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	setDecision(c, Decision{Language: LangEnUS, Source: SourceTrustedHeader})
	PromoteUserLanguage(c, "zh-CN")
	if d := FromGin(c); d.Language != LangEnUS || d.Source != SourceTrustedHeader {
		t.Errorf("trusted header should not be overridden by user pref: got %+v", d)
	}
}

func TestEarlyMiddlewareSetsContentLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Accept-Language", "en-US")
	EarlyMiddleware()(c)
	if got := w.Header().Get("Content-Language"); got != LangEnUS {
		t.Errorf("Content-Language = %q, want %q", got, LangEnUS)
	}
	if d := FromGin(c); d.Language != LangEnUS {
		t.Errorf("decision language = %q, want %q", d.Language, LangEnUS)
	}
}

func TestRespondErrorLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	setDecision(c, Decision{Language: LangEnUS, Source: SourceAccept})
	RespondError(c, http.StatusNotFound, "MATTER_NOT_FOUND", KeyMatterNotFound, nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if body := w.Body.String(); body == "" ||
		!containsAll(body, `"code":"MATTER_NOT_FOUND"`, `"message":"matter not found"`) {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
