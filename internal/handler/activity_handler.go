// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

// activityLister is the narrow ActivityService surface the handler depends on.
type activityLister interface {
	ListActivities(
		ctx context.Context,
		matterID, spaceID string,
		callerUIDs []string,
		cursor *string,
		limit int,
	) ([]*model.MatterActivity, bool, error)
}

// ActivityHandler serves GET /api/v1/matters/:id/activities.
type ActivityHandler struct {
	svc activityLister
}

// NewActivityHandler wires the activity handler.
func NewActivityHandler(svc activityLister) *ActivityHandler {
	return &ActivityHandler{svc: svc}
}

// List handles GET /api/v1/matters/:id/activities.
func (h *ActivityHandler) List(c *gin.Context) {
	matterID := c.Param("id")
	if !validUUID(matterID) {
		failCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid id format", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var cursor *string
	if cur := c.Query("cursor"); cur != "" {
		cursor = &cur
	}
	// NOTE: source_channel_id is intentionally NOT accepted here. Activities
	// are a matter-global audit log; admitting channel-member-only callers
	// would leak cross-channel activity. See PR #39 review r4259484583.
	items, hasMore, err := h.svc.ListActivities(
		c.Request.Context(),
		matterID,
		spaceID(c),
		relatedUIDs(c),
		cursor,
		limit,
	)
	if err != nil {
		respondErr(c, err)
		return
	}
	var nextCursor string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	paginated(c, items, hasMore, nextCursor)
}
