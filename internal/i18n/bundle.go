package i18n

import (
	"embed"
	"sync"

	"github.com/BurntSushi/toml"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.toml
var localesFS embed.FS

// Supported runtime languages. en-US is the source; zh-CN the translation.
const (
	LangZhCN = "zh-CN"
	LangEnUS = "en-US"
)

var supported = []string{LangZhCN, LangEnUS}

var (
	bundle *goi18n.Bundle
	// defaultLang is the runtime fallback used when negotiation yields no
	// signal. Set via Init (from OCTO_DEFAULT_LANGUAGE); zh-CN until then.
	defaultLang = LangZhCN
	initOnce    sync.Once
)

// Init builds the message bundle and records the default fallback language.
// Safe to call once at startup; subsequent calls are no-ops. An invalid
// defaultLanguage is rejected by config.Validate before this is reached.
func Init(defaultLanguage string) {
	initOnce.Do(func() {
		if MatchSupported(defaultLanguage) != "" {
			defaultLang = MatchSupported(defaultLanguage)
		}
		b := goi18n.NewBundle(language.AmericanEnglish)
		b.RegisterUnmarshalFunc("toml", toml.Unmarshal)
		for _, name := range []string{"locales/active.en-US.toml", "locales/active.zh-CN.toml"} {
			data, err := localesFS.ReadFile(name)
			if err != nil {
				panic("i18n: cannot read embedded locale " + name + ": " + err.Error())
			}
			if _, err := b.ParseMessageFileBytes(data, name); err != nil {
				panic("i18n: cannot parse locale " + name + ": " + err.Error())
			}
		}
		bundle = b
	})
}

// ensure lazily initializes with defaults so callers (e.g. tests) that skip
// Init still get a working localizer.
func ensure() {
	if bundle == nil {
		Init(defaultLang)
	}
}

// DefaultLanguage returns the configured runtime fallback language.
func DefaultLanguage() string { return defaultLang }

// SupportedLanguages returns the runtime-supported language tags.
func SupportedLanguages() []string { return append([]string(nil), supported...) }

// Localize renders msgID in lang with the given template params. It falls back
// lang -> defaultLang -> en-US source -> msgID itself, so a missing key never
// produces an empty string.
func Localize(lang, msgID string, params map[string]any) string {
	ensure()
	if lang == "" {
		lang = defaultLang
	}
	loc := goi18n.NewLocalizer(bundle, lang, defaultLang, LangEnUS)
	out, err := loc.Localize(&goi18n.LocalizeConfig{
		MessageID:    msgID,
		TemplateData: params,
	})
	if err != nil || out == "" {
		return msgID
	}
	return out
}
