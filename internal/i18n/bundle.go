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
	// localizers holds one cached *Localizer per supported language so hot
	// paths (error rendering) don't allocate a new one per call. Populated once
	// under initOnce, then read-only — so reads need no lock.
	localizers map[string]*goi18n.Localizer
	// defaultLang is the runtime fallback used when negotiation yields no
	// signal. Set via Init (from OCTO_DEFAULT_LANGUAGE); zh-CN until then.
	defaultLang = LangZhCN
	initOnce    sync.Once
)

// Init builds the message bundle and records the default fallback language.
// Safe to call repeatedly; only the first call does the work (sync.Once), which
// also establishes the happens-before for lock-free reads of localizers.
// An invalid defaultLanguage is rejected by config.Validate before this runs.
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
		// One localizer per supported language, each with a fallback chain of
		// [lang, defaultLang, en-US source].
		localizers = make(map[string]*goi18n.Localizer, len(supported))
		for _, lang := range supported {
			localizers[lang] = goi18n.NewLocalizer(b, lang, defaultLang, LangEnUS)
		}
	})
}

// ensure initializes with package defaults for callers (e.g. tests) that skip
// Init. Calling Init unconditionally — rather than guarding on a racy nil read
// — routes every caller through sync.Once, giving safe lock-free reads after.
func ensure() {
	Init(defaultLang)
}

// localizerFor returns the cached localizer for lang, falling back to the
// default-language localizer for empty/unsupported input.
func localizerFor(lang string) *goi18n.Localizer {
	if loc, ok := localizers[lang]; ok {
		return loc
	}
	return localizers[defaultLang]
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
	out, err := localizerFor(lang).Localize(&goi18n.LocalizeConfig{
		MessageID:    msgID,
		TemplateData: params,
	})
	if err != nil || out == "" {
		return msgID
	}
	return out
}
