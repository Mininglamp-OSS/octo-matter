// Dual-protocol auth middleware: tries daemon JWT first, then falls back
// to X-Internal-Token. Lets matter accept BOTH the legacy shared-secret
// callers (fleet bot-feed proxy, server notify) AND new daemon-JWT
// callers (daemon timeline/activity writeback) on the same endpoint
// without forcing any client to switch in lockstep.
//
// Design: see plan §7 ("matter writeback 也切 JWT"). The shared-secret
// path stays alive indefinitely for callers that have no JWT (fleet
// proxies user requests, not daemons; server lacks a daemon scope).
package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// DualAuth chooses an auth path per request by header shape:
//   - Authorization: Bearer <jwt>  → jwtMW
//   - anything else                → internalTokenMW
//
// Each inner middleware aborts on failure or calls c.Next() on success,
// so this wrapper just delegates; no double-handler-run risk because gin
// only advances the chain via c.Next().
func DualAuth(jwtMW, internalTokenMW gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
			jwtMW(c)
			return
		}
		internalTokenMW(c)
	}
}
