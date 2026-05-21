package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/gin-gonic/gin"
)

// outputsLister is the narrow OutputsService surface the handler depends on.
type outputsLister interface {
	ListOutputs(
		ctx context.Context,
		matterID, spaceID string,
		callerUIDs []string,
		q string,
		cursor *string,
		limit int,
	) ([]*model.MatterOutput, bool, error)
}

// OutputsHandler serves GET /api/v1/matters/:id/outputs — the "matter outputs"
// view: deduplicated file attachments uploaded to the matter, ordered by IM
// message time desc.
type OutputsHandler struct {
	svc outputsLister
}

func NewOutputsHandler(svc outputsLister) *OutputsHandler {
	return &OutputsHandler{svc: svc}
}

// outputsMaxQLen caps the q filter length so a hostile caller cannot push
// a giant LIKE pattern through the index.
const outputsMaxQLen = 100

// List handles GET /api/v1/matters/:id/outputs.
//
// Query params:
//   - limit  (1..200, default 50)
//   - cursor (opaque, from a previous response)
//   - q      (optional, filename / description / sender name LIKE)
func (h *OutputsHandler) List(c *gin.Context) {
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
	// Clip by rune count, not byte count — a UTF-8 byte slice can split
	// mid-rune and produce invalid utf8mb4 that MySQL rejects with 1366,
	// turning a bounded query into a 500.
	q := strings.TrimSpace(c.Query("q"))
	if r := []rune(q); len(r) > outputsMaxQLen {
		q = string(r[:outputsMaxQLen])
	}

	items, hasMore, err := h.svc.ListOutputs(
		c.Request.Context(),
		matterID,
		spaceID(c),
		relatedUIDs(c),
		q,
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
			CreatedAt: last.SentAt,
			ID:        last.ID,
		})
	}
	paginated(c, items, hasMore, nextCursor)
}
