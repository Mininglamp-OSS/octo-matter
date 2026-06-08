package i18n

import "github.com/gin-gonic/gin"

// EarlyMiddleware negotiates the request language from request-level signals
// (before auth) and stores it on the gin context. It also sets Content-Language
// and Vary so caches key on the negotiation inputs. AuthMiddleware may later
// promote a higher-priority user.language via PromoteUserLanguage.
func EarlyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		d := Negotiate(c.Request)
		setDecision(c, d)
		h := c.Writer.Header()
		h.Set("Content-Language", d.Language)
		h.Add("Vary", "Accept-Language")
		h.Add("Vary", HeaderOctoLang)
		h.Add("Vary", "Cookie")
		c.Next()
	}
}
