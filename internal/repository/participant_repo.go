package repository

import (
	"time"

	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

type ParticipantRepo struct {
	runner dbr.SessionRunner
}

func NewParticipantRepo(sess *dbr.Session) *ParticipantRepo {
	return &ParticipantRepo{runner: sess}
}

// Upsert idempotently inserts a participant row (INSERT IGNORE).
func (r *ParticipantRepo) Upsert(matterID, userID string) error {
	_, err := r.runner.InsertBySql(
		"INSERT IGNORE INTO matter_participants (id, matter_id, user_id, created_at) VALUES (?, ?, ?, ?)",
		uuid.New().String(), matterID, userID, time.Now(),
	).Exec()
	return err
}

// IsParticipantAny reports whether any of userIDs is a participant of matterID.
func (r *ParticipantRepo) IsParticipantAny(matterID string, userIDs []string) (bool, error) {
	if len(userIDs) == 0 {
		return false, nil
	}
	count, err := r.runner.Select("COUNT(*)").
		From("matter_participants").
		Where("matter_id = ? AND user_id IN ?", matterID, userIDs).
		ReturnInt64()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListUserIDs returns every participant uid for a matter.
func (r *ParticipantRepo) ListUserIDs(matterID string) ([]string, error) {
	uids := make([]string, 0)
	_, err := r.runner.Select("user_id").
		From("matter_participants").
		Where("matter_id = ?", matterID).
		Load(&uids)
	if err != nil {
		return nil, err
	}
	return uids, nil
}
