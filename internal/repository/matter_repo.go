package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type MatterFilter struct {
	CallerUIDs        []string
	Status            *string
	AssigneeID        *string
	CreatorID         *string
	SourceChannelID   *string
	SourceChannelType *uint8
	DeadlineBefore    *time.Time
	DeadlineAfter     *time.Time
	Query             *string
	Cursor            *string
	Limit             int
}

type MatterRepo struct {
	runner dbr.SessionRunner
}

func NewMatterRepo(sess *dbr.Session) *MatterRepo {
	return &MatterRepo{runner: sess}
}

func (r *MatterRepo) Create(ctx context.Context, matter *model.Matter) error {
	matter.ID = uuid.New().String()
	now := time.Now()
	matter.CreatedAt = now
	matter.UpdatedAt = now
	_, err := r.runner.InsertInto("matters").
		Columns("id", "space_id", "title", "description", "creator_id",
			"status", "deadline", "remind_at", "source_channel_id", "source_channel_type",
			"source_name", "created_at", "updated_at", "deleted_at").
		Record(matter).
		ExecContext(ctx)
	return err
}

func (r *MatterRepo) GetByID(ctx context.Context, id, spaceID string) (*model.Matter, error) {
	var matter model.Matter
	err := r.runner.Select("*").
		From("matters").
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		LoadOneContext(ctx, &matter)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.MatterNotFound()
		}
		return nil, err
	}
	return &matter, nil
}

func (r *MatterRepo) ListBySpace(ctx context.Context, spaceID string, filter MatterFilter) ([]*model.Matter, bool, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	q := r.runner.Select("*").
		From("matters").
		Where("space_id = ? AND deleted_at IS NULL", spaceID)

	// Visibility: user must be creator, assignee, or participant. When a
	// source_channel_id is provided, matters whose linked channels include
	// that channel id are also visible.
	if filter.SourceChannelID != nil {
		q = q.Where(
			"(creator_id IN ?"+
				" OR EXISTS (SELECT 1 FROM matter_assignees WHERE matter_assignees.matter_id = matters.id AND matter_assignees.user_id IN ?)"+
				" OR EXISTS (SELECT 1 FROM matter_participants WHERE matter_participants.matter_id = matters.id AND matter_participants.user_id IN ?)"+
				" OR EXISTS (SELECT 1 FROM matter_channels WHERE matter_channels.matter_id = matters.id AND matter_channels.channel_id = ?))",
			filter.CallerUIDs, filter.CallerUIDs, filter.CallerUIDs, *filter.SourceChannelID,
		)
	} else {
		q = q.Where(
			"(creator_id IN ?"+
				" OR EXISTS (SELECT 1 FROM matter_assignees WHERE matter_assignees.matter_id = matters.id AND matter_assignees.user_id IN ?)"+
				" OR EXISTS (SELECT 1 FROM matter_participants WHERE matter_participants.matter_id = matters.id AND matter_participants.user_id IN ?))",
			filter.CallerUIDs, filter.CallerUIDs, filter.CallerUIDs,
		)
	}

	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
	}
	if filter.AssigneeID != nil {
		q = q.Where("id IN (SELECT matter_id FROM matter_assignees WHERE user_id = ?)", *filter.AssigneeID)
	}
	if filter.CreatorID != nil {
		q = q.Where("creator_id = ?", *filter.CreatorID)
	}
	if filter.SourceChannelType != nil {
		q = q.Where("source_channel_type = ?", *filter.SourceChannelType)
	}
	if filter.DeadlineBefore != nil {
		q = q.Where("deadline < ?", *filter.DeadlineBefore)
	}
	if filter.DeadlineAfter != nil {
		q = q.Where("deadline > ?", *filter.DeadlineAfter)
	}
	if filter.Query != nil && *filter.Query != "" {
		escaped := escapeLikePattern(*filter.Query)
		q = q.Where("title LIKE ?", "%"+escaped+"%")
	}

	if filter.Cursor != nil && *filter.Cursor != "" {
		cur, err := DecodeCursor(*filter.Cursor)
		if err != nil {
			return nil, false, err
		}
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", cur.CreatedAt, cur.CreatedAt, cur.ID)
	}

	var matters []*model.Matter
	_, err := q.OrderBy("created_at DESC").
		OrderBy("id DESC").
		Limit(uint64(limit + 1)).
		LoadContext(ctx, &matters)
	if err != nil {
		return nil, false, err
	}
	if matters == nil {
		matters = make([]*model.Matter, 0)
	}

	hasMore := len(matters) > limit
	if hasMore {
		matters = matters[:limit]
	}
	return matters, hasMore, nil
}

func (r *MatterRepo) GetByIDForUpdate(ctx context.Context, id, spaceID string) (*model.Matter, error) {
	var matter model.Matter
	err := r.runner.SelectBySql(
		"SELECT * FROM matters WHERE id = ? AND space_id = ? AND deleted_at IS NULL FOR UPDATE",
		id, spaceID,
	).LoadOneContext(ctx, &matter)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return nil, apperr.MatterNotFound()
		}
		return nil, err
	}
	return &matter, nil
}

func (r *MatterRepo) Update(ctx context.Context, matter *model.Matter) error {
	matter.UpdatedAt = time.Now()
	result, err := r.runner.Update("matters").
		Set("title", matter.Title).
		Set("description", matter.Description).
		Set("deadline", matter.Deadline).
		Set("remind_at", matter.RemindAt).
		Set("updated_at", matter.UpdatedAt).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", matter.ID, matter.SpaceID).
		ExecContext(ctx)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return apperr.MatterNotFound()
	}
	return nil
}

func (r *MatterRepo) UpdateStatus(ctx context.Context, id, spaceID, status string) error {
	result, err := r.runner.Update("matters").
		Set("status", status).
		Set("updated_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		ExecContext(ctx)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return apperr.MatterNotFound()
	}
	return nil
}

func (r *MatterRepo) SoftDelete(ctx context.Context, id, spaceID string) error {
	result, err := r.runner.Update("matters").
		Set("deleted_at", time.Now()).
		Where("id = ? AND space_id = ? AND deleted_at IS NULL", id, spaceID).
		ExecContext(ctx)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return apperr.MatterNotFound()
	}
	return nil
}

func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// HasAccess checks in a single query whether any of callerUIDs has access to
// the matter via assignee or participant role, or whether channelID is linked.
// Creator check is done in-memory by the caller so not included here.
func (r *MatterRepo) HasAccess(ctx context.Context, matterID string, callerUIDs []string, channelID string) (bool, error) {
	if len(callerUIDs) == 0 && channelID == "" {
		return false, nil
	}
	q := r.runner.Select("1")

	if channelID != "" && len(callerUIDs) > 0 {
		q = q.From("dual").Where(
			`(EXISTS (SELECT 1 FROM matter_assignees WHERE matter_id = ? AND user_id IN ?)
			  OR EXISTS (SELECT 1 FROM matter_participants WHERE matter_id = ? AND user_id IN ?)
			  OR EXISTS (SELECT 1 FROM matter_channels WHERE matter_id = ? AND channel_id = ?))`,
			matterID, callerUIDs, matterID, callerUIDs, matterID, channelID,
		)
	} else if len(callerUIDs) > 0 {
		q = q.From("dual").Where(
			`(EXISTS (SELECT 1 FROM matter_assignees WHERE matter_id = ? AND user_id IN ?)
			  OR EXISTS (SELECT 1 FROM matter_participants WHERE matter_id = ? AND user_id IN ?))`,
			matterID, callerUIDs, matterID, callerUIDs,
		)
	} else {
		q = q.From("dual").Where(
			`EXISTS (SELECT 1 FROM matter_channels WHERE matter_id = ? AND channel_id = ?)`,
			matterID, channelID,
		)
	}

	var dummy int
	err := q.LoadOneContext(ctx, &dummy)
	if err != nil {
		if errors.Is(err, dbr.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
