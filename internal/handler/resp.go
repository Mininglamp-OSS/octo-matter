package handler

import (
	"errors"
	"log"
	"net/http"
	"regexp"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validUUID(id string) bool {
	return uuidRegex.MatchString(id)
}

func ok(c *gin.Context, data any) {
	if data == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, data)
}

func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, data)
}

func paginated(c *gin.Context, data any, hasMore bool, nextCursor string) {
	pg := gin.H{"has_more": hasMore}
	if hasMore && nextCursor != "" {
		pg["next_cursor"] = nextCursor
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "pagination": pg})
}

// failKey writes a localized error: msgID is resolved against the request
// language by the i18n responder.
func failKey(c *gin.Context, status int, code, msgID string, details map[string]any) {
	i18n.RespondError(c, status, code, msgID, nil, details)
}

func respondErr(c *gin.Context, err error) {
	if ae, ok := apperr.AsAppError(err); ok {
		i18n.RespondError(c, ae.HTTPStatus(), ae.Code(), ae.MessageID(), ae.Params(), ae.Details())
		return
	}
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		failKey(c, http.StatusNotFound, "NOT_FOUND", i18n.KeyNotFound, nil)
	case errors.Is(err, apperr.ErrForbidden):
		failKey(c, http.StatusForbidden, "FORBIDDEN", i18n.KeyForbidden, nil)
	case errors.Is(err, apperr.ErrInvalidInput):
		failKey(c, http.StatusBadRequest, "VALIDATION_ERROR", i18n.KeyInvalidRequest, nil)
	case errors.Is(err, repository.ErrInvalidCursor):
		failKey(c, http.StatusBadRequest, "VALIDATION_ERROR", i18n.KeyInvalidCursor, nil)
	default:
		log.Printf("internal error: %v", err)
		i18n.RespondError(c, http.StatusInternalServerError, "INTERNAL_ERROR", i18n.KeyInternal, nil, nil)
	}
}

func bindJSONErr(c *gin.Context, err error) {
	if err.Error() == "http: request body too large" {
		failKey(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", i18n.KeyPayloadTooLarge, nil)
		return
	}
	// The raw validator/bind error is English and may name internal struct
	// fields, so it is logged server-side rather than echoed to the client.
	// The response carries only the localized generic message; field-level
	// localization (validator tag -> key) is deferred.
	log.Printf("bind error: %v", err)
	failKey(c, http.StatusBadRequest, "VALIDATION_ERROR", i18n.KeyInvalidRequest, nil)
}

func uid(c *gin.Context) string {
	return c.GetString("uid")
}

func userName(c *gin.Context) string {
	name := c.GetString("name")
	if name == "" {
		return c.GetString("uid")
	}
	return name
}

// actorNameFor returns the human-friendly name for actorUID, taking the
// caller context into account. When a bot acts on behalf of its owner
// (LLM-mediated path: actorUID == owner_uid), the notification name is the
// owner's name rather than the bot's — DB stores the owner as author, so
// the push notification must agree.
//
// For all other cases (user path; bot acting as itself) it returns the
// caller's own name via userName.
func actorNameFor(c *gin.Context, actorUID string) string {
	if c.GetString("role") == "bot" && actorUID != "" && actorUID == c.GetString("owner_uid") {
		if name := c.GetString("owner_name"); name != "" {
			return name
		}
		return actorUID
	}
	return userName(c)
}

func spaceID(c *gin.Context) string {
	return c.GetString("space_id")
}

func relatedUIDs(c *gin.Context) []string {
	if v, exists := c.Get("related_uids"); exists {
		if uids, ok := v.([]string); ok && len(uids) > 0 {
			return uids
		}
	}
	return []string{uid(c)}
}

// callerToken returns the user's IM auth token for forwarding to octoim
// channel-membership lookups. Returns "" for bot callers (bots are
// authenticated via Bearer + verify-bot, not the public token used by the
// channel members API; see PR#34 thread for the trust model). Service-layer
// access checks treat "" as "skip IM channel-membership verification".
func callerToken(c *gin.Context) string {
	if c.GetString("role") == "bot" {
		return ""
	}
	return c.GetHeader("token")
}

// effectiveCallerUIDs returns the UIDs whose permissions apply to this request.
// For user auth this is [self, owned_bots...]; for bot auth it is [bot, owner]
// so service-layer access checks grant the bot the same write power as its
// owner (per v3 design §5).
func effectiveCallerUIDs(c *gin.Context) []string {
	base := relatedUIDs(c)
	if c.GetString("role") != "bot" {
		return base
	}
	owner := c.GetString("owner_uid")
	if owner == "" {
		return base
	}
	for _, u := range base {
		if u == owner {
			return base
		}
	}
	return append(append([]string{}, base...), owner)
}
