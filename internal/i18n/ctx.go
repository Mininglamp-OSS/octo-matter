package i18n

import (
	"context"

	"github.com/gin-gonic/gin"
)

// ginKey is the gin.Context key holding the negotiated Decision.
const ginKey = "i18n_decision"

// ctxKeyType is an unexported type for the context.Context language key, so it
// cannot collide with keys from other packages.
type ctxKeyType struct{}

var ctxKey = ctxKeyType{}

// WithLanguage returns a copy of ctx carrying the negotiated decision, so code
// running below the handler (services, detached worker goroutines that inherit
// the request context) can localize in the requester's language.
func WithLanguage(ctx context.Context, d Decision) context.Context {
	return context.WithValue(ctx, ctxKey, d)
}

// FromContext returns the decision carried by ctx, or the default-language
// decision if none is present.
func FromContext(ctx context.Context) Decision {
	if ctx != nil {
		if d, ok := ctx.Value(ctxKey).(Decision); ok {
			return d
		}
	}
	return Decision{Language: defaultLang, Source: SourceDefault}
}

// LangFromContext is a convenience wrapper returning just the language tag.
func LangFromContext(ctx context.Context) string { return FromContext(ctx).Language }

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
		d := Decision{Language: lang, Source: SourceUser}
		setDecision(c, d)
		c.Request = c.Request.WithContext(WithLanguage(c.Request.Context(), d))
		c.Writer.Header().Set("Content-Language", lang)
	}
}
