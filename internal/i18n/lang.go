package i18n

import (
	"net/http"
	"strings"

	"golang.org/x/text/language"
)

// Request-level language signals, aligned with octo-server pkg/i18n.
const (
	// HeaderOctoLang is the trusted gateway-set language header (highest
	// priority among request signals).
	HeaderOctoLang = "X-Octo-Lang"
	// CookieLanguage is the frontend/static-page language cookie.
	CookieLanguage = "i18n_lang"
	// QueryLanguage is the explicit URL language selector.
	QueryLanguage = "lang"
)

// LanguageSource records which negotiation stage produced the decision. Lower
// numeric value = higher priority, so a late-merge only overrides when the new
// source ranks strictly higher.
type LanguageSource int

const (
	SourceTrustedHeader LanguageSource = iota
	SourceQuery
	SourceCookie
	SourceUser
	SourceAccept
	SourceDefault
)

// Decision is the negotiated language plus the source it came from.
type Decision struct {
	Language string
	Source   LanguageSource
}

var matcher = language.NewMatcher([]language.Tag{
	language.AmericanEnglish,   // en-US
	language.SimplifiedChinese, // zh-CN
})

// MatchSupported normalizes raw (a BCP-47 tag, possibly with quality/region
// variants) to one of the supported tags, or "" if it cannot be matched.
func MatchSupported(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	switch strings.ToLower(raw) {
	case "zh-cn", "zh", "zh-hans", "zh-hans-cn":
		return LangZhCN
	case "en-us", "en", "en-gb":
		return LangEnUS
	}
	tags, _, err := language.ParseAcceptLanguage(raw)
	if err != nil || len(tags) == 0 {
		return ""
	}
	_, idx, conf := matcher.Match(tags...)
	if conf == language.No {
		return ""
	}
	switch idx {
	case 0:
		return LangEnUS
	case 1:
		return LangZhCN
	}
	return ""
}

// Negotiate resolves the request language from the available signals using the
// priority X-Octo-Lang > ?lang > cookie > Accept-Language > default. The
// user.language stage is applied later via WithLanguageIfHigherPriority once
// auth has run.
func Negotiate(r *http.Request) Decision {
	if lang := MatchSupported(r.Header.Get(HeaderOctoLang)); lang != "" {
		return Decision{Language: lang, Source: SourceTrustedHeader}
	}
	if lang := MatchSupported(r.URL.Query().Get(QueryLanguage)); lang != "" {
		return Decision{Language: lang, Source: SourceQuery}
	}
	if ck, err := r.Cookie(CookieLanguage); err == nil {
		if lang := MatchSupported(ck.Value); lang != "" {
			return Decision{Language: lang, Source: SourceCookie}
		}
	}
	if lang := MatchSupported(r.Header.Get("Accept-Language")); lang != "" {
		return Decision{Language: lang, Source: SourceAccept}
	}
	return Decision{Language: defaultLang, Source: SourceDefault}
}
