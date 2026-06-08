package i18n

import "github.com/gin-gonic/gin"

// ginKey is the gin.Context key holding the negotiated Decision.
const ginKey = "i18n_decision"

// setDecision stores the decision on the gin context.
func setDecision(c *gin.Context, d Decision) {
	c.Set(ginKey, d)
}

// FromGin returns the negotiated decision, or the default-language decision if
// none was set (e.g. routes without the middleware).
func FromGin(c *gin.Context) Decision {
	if c == nil {
		return Decision{Language: defaultLang, Source: SourceDefault}
	}
	if v, ok := c.Get(ginKey); ok {
		if d, ok := v.(Decision); ok {
			return d
		}
	}
	return Decision{Language: defaultLang, Source: SourceDefault}
}

// LangFromGin is a convenience wrapper returning just the language tag.
func LangFromGin(c *gin.Context) string { return FromGin(c).Language }

// PromoteUserLanguage applies a user.language preference (resolved after auth)
// as a SourceUser decision, but only when it outranks the current source. This
// mirrors octo-server's two-stage merge: an explicit X-Octo-Lang / ?lang /
// cookie choice still wins over the stored user preference.
func PromoteUserLanguage(c *gin.Context, raw string) {
	lang := MatchSupported(raw)
	if lang == "" {
		return
	}
	cur := FromGin(c)
	if SourceUser < cur.Source {
		setDecision(c, Decision{Language: lang, Source: SourceUser})
		c.Writer.Header().Set("Content-Language", lang)
	}
}
