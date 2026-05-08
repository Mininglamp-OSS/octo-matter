package repository

import (
	"context"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type MatterChannelRepo struct {
	runner dbr.SessionRunner
}

func NewMatterChannelRepo(sess *dbr.Session) *MatterChannelRepo {
	return &MatterChannelRepo{runner: sess}
}

func (r *MatterChannelRepo) Create(ctx context.Context, mc *model.MatterChannel) error {
	if mc.ID == "" {
		mc.ID = uuid.New().String()
	}
	if mc.CreatedAt.IsZero() {
		mc.CreatedAt = time.Now()
	}
	_, err := r.runner.InsertInto("matter_channels").
		Columns("id", "matter_id", "channel_id", "channel_type", "channel_name", "linked_by", "created_at").
		Record(mc).
		ExecContext(ctx)
	if err != nil && isDuplicateKeyErr(err) {
		return nil
	}
	return err
}

func (r *MatterChannelRepo) Delete(ctx context.Context, matterID, channelID string) error {
	_, err := r.runner.DeleteFrom("matter_channels").
		Where("matter_id = ? AND channel_id = ?", matterID, channelID).
		ExecContext(ctx)
	return err
}

func (r *MatterChannelRepo) IsLinkedChannel(ctx context.Context, matterID, channelID string) (bool, error) {
	if channelID == "" {
		return false, nil
	}
	count, err := r.runner.Select("COUNT(*)").
		From("matter_channels").
		Where("matter_id = ? AND channel_id = ?", matterID, channelID).
		ReturnInt64Context(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *MatterChannelRepo) ListByMatter(ctx context.Context, matterID string) ([]*model.MatterChannel, error) {
	channels := make([]*model.MatterChannel, 0)
	_, err := r.runner.Select("*").
		From("matter_channels").
		Where("matter_id = ?", matterID).
		LoadContext(ctx, &channels)
	if err != nil {
		return nil, err
	}
	return channels, nil
}
