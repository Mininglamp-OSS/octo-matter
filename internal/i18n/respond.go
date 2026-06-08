package i18n

import "github.com/gin-gonic/gin"

// RespondError writes the localized REST error envelope and aborts the chain.
// It preserves octo-matter's existing shape:
//
//	{"error":{"code":<code>,"message":<localized>,"details":<details?>}}
//
// The language is read from the gin context (set by EarlyMiddleware); msgID is
// localized with params. Used by the handler layer and the auth/rate-limit
// middlewares alike so every error response is consistent and localized.
func RespondError(c *gin.Context, status int, code, msgID string, params, details map[string]any) {
	msg := Localize(LangFromGin(c), msgID, params)
	body := gin.H{"code": code, "message": msg}
	if len(details) > 0 {
		body["details"] = details
	}
	c.AbortWithStatusJSON(status, gin.H{"error": body})
}
