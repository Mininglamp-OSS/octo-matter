package service

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// matterStore is the narrow MatterRepo surface MatterService depends on.
type matterStore interface {
	Create(matter *model.Matter) error
	GetByID(id, spaceID string) (*model.Matter, error)
	ListBySpace(spaceID string, filter repository.MatterFilter) ([]*model.Matter, bool, error)
	Update(matter *model.Matter) error
	UpdateStatus(id, spaceID, status string) error
	SoftDelete(id, spaceID string) error
}

type assigneeStore interface {
	Create(a *model.MatterAssignee) error
	Delete(matterID, userID string) error
	ListByMatter(matterID string) ([]*model.MatterAssignee, error)
	IsAssignee(matterID, userID string) (bool, error)
}

// txRunner runs a closure against a bundle of tx-bound repos.
type txRunner interface {
	Do(fn func(r *repository.TxRepos) error) error
}

type MatterService struct {
	matterRepo   matterStore
	assigneeRepo assigneeStore
	tx           txRunner
}

func NewMatterService(matterRepo matterStore, assigneeRepo assigneeStore, tx txRunner) *MatterService {
	return &MatterService{matterRepo: matterRepo, assigneeRepo: assigneeRepo, tx: tx}
}

// CreateMatterWithAssignees creates the matter and all its initial assignees inside
// a single transaction.
func (s *MatterService) CreateMatterWithAssignees(matter *model.Matter, assigneeIDs []string) (*MatterDetail, error) {
	if matter.Status == "" {
		matter.Status = model.MatterStatusOpen
	}
	var created []*model.MatterAssignee
	err := s.tx.Do(func(r *repository.TxRepos) error {
		if err := r.Matter.Create(matter); err != nil {
			return err
		}
		created = make([]*model.MatterAssignee, 0, len(assigneeIDs))
		for _, aid := range assigneeIDs {
			a := &model.MatterAssignee{
				MatterID: matter.ID,
				UserID:   aid,
			}
			if err := r.Assignee.Create(a); err != nil {
				return err
			}
			created = append(created, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &MatterDetail{
		Matter:    matter,
		Assignees: created,
	}, nil
}

type MatterListResult struct {
	Items      []*model.Matter `json:"items"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func (s *MatterService) ListMatters(spaceID string, filter repository.MatterFilter) (*MatterListResult, error) {
	matters, hasMore, err := s.matterRepo.ListBySpace(spaceID, filter)
	if err != nil {
		return nil, err
	}
	var nextCursor string
	if hasMore && len(matters) > 0 {
		last := matters[len(matters)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	return &MatterListResult{
		Items:      matters,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// MatterDetail is the enriched response for a single matter.
type MatterDetail struct {
	*model.Matter
	Assignees []*model.MatterAssignee `json:"assignees"`
}

// GetMatterForNotification loads a matter by ID+space without access checks.
func (s *MatterService) GetMatterForNotification(id, spaceID string) (*model.Matter, error) {
	return s.matterRepo.GetByID(id, spaceID)
}

// GetMatter loads a matter the caller is allowed to read.
func (s *MatterService) GetMatter(id, spaceID, userID, sourceChannelID string) (*MatterDetail, error) {
	matter, err := s.matterRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.canAccessMatter(matter, userID, sourceChannelID) {
		return nil, apperr.Forbidden("not authorized to view this matter")
	}
	assignees, err := s.assigneeRepo.ListByMatter(id)
	if err != nil {
		return nil, err
	}
	return &MatterDetail{
		Matter:    matter,
		Assignees: assignees,
	}, nil
}

// CanAccessMatter checks if user can view a matter: creator, assignee,
// or — when sourceChannelID is non-empty — a member of the matter's
// originating channel.
func (s *MatterService) CanAccessMatter(matter *model.Matter, userID string, sourceChannelID string) bool {
	return s.canAccessMatter(matter, userID, sourceChannelID)
}

func (s *MatterService) canAccessMatter(matter *model.Matter, userID string, sourceChannelID string) bool {
	if sourceChannelID != "" && matter.SourceChannelID != nil && *matter.SourceChannelID == sourceChannelID {
		return true
	}
	if matter.CreatorID == userID {
		return true
	}
	if ok, _ := s.assigneeRepo.IsAssignee(matter.ID, userID); ok {
		return true
	}
	return false
}

// UpdateMatter applies editable fields.
func (s *MatterService) UpdateMatter(id, spaceID, userID string, title *string, description *string, deadline, remindAt *string) (*model.Matter, error) {
	matter, err := s.matterRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	// Creator or assignee can update a matter.
	isCreator := matter.CreatorID == userID
	isAssignee, _ := s.assigneeRepo.IsAssignee(id, userID)
	if !isCreator && !isAssignee {
		return nil, apperr.ErrForbidden
	}
	if title != nil {
		matter.Title = *title
	}
	if description != nil {
		matter.Description = description
	}
	if deadline != nil {
		t, err := ParseOptionalRFC3339(*deadline)
		if err != nil {
			return nil, apperr.InvalidInput("deadline must be RFC3339 or empty")
		}
		matter.Deadline = t
	}
	if remindAt != nil {
		t, err := ParseOptionalRFC3339(*remindAt)
		if err != nil {
			return nil, apperr.InvalidInput("remind_at must be RFC3339 or empty")
		}
		matter.RemindAt = t
	}
	if err := s.matterRepo.Update(matter); err != nil {
		return nil, err
	}
	return matter, nil
}

// ParseOptionalRFC3339 returns (nil, nil) for the empty string (clear the
// field) and otherwise parses as RFC3339.
func ParseOptionalRFC3339(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SetStatus changes a matter's status. No state machine — creator or assignee
// can change status freely. Uses a transaction with SELECT FOR UPDATE.
func (s *MatterService) SetStatus(id, spaceID, userID string, target model.MatterStatus) (*MatterDetail, error) {
	if !model.IsValidStatus(target) {
		return nil, apperr.InvalidInput("status must be 'open', 'done', or 'archived'")
	}

	err := s.tx.Do(func(r *repository.TxRepos) error {
		matter, err := r.Matter.GetByIDForUpdate(id, spaceID)
		if err != nil {
			return err
		}

		// Creator or assignee can change status.
		isCreator := matter.CreatorID == userID
		isAssignee, _ := r.Assignee.IsAssignee(id, userID)
		if !isCreator && !isAssignee {
			return apperr.Forbidden("only creator or assignee can change status")
		}

		if matter.Status == target {
			return nil // idempotent — no-op
		}

		// archived is terminal — can only reopen (→ open), not go to done
		if matter.Status == model.MatterStatusArchived && target == model.MatterStatusDone {
			return apperr.InvalidInput("cannot transition from archived to done; reopen first")
		}

		if err := r.Matter.UpdateStatus(id, spaceID, string(target)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Re-read outside tx for fresh response.
	matter, err := s.matterRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	assignees, err := s.assigneeRepo.ListByMatter(id)
	if err != nil {
		return nil, err
	}
	return &MatterDetail{
		Matter:    matter,
		Assignees: assignees,
	}, nil
}

func (s *MatterService) SoftDelete(id, spaceID, userID string) error {
	matter, err := s.matterRepo.GetByID(id, spaceID)
	if err != nil {
		return err
	}
	if matter.CreatorID != userID {
		return apperr.ErrForbidden
	}
	return s.matterRepo.SoftDelete(id, spaceID)
}

// ListAssigneeIDs returns the user IDs of all assignees for a matter.
func (s *MatterService) ListAssigneeIDs(matterID string) ([]string, error) {
	assignees, err := s.assigneeRepo.ListByMatter(matterID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(assignees))
	for _, a := range assignees {
		ids = append(ids, a.UserID)
	}
	return ids, nil
}

func (s *MatterService) AddAssignee(matterID, spaceID, userID, assigneeUserID string) error {
	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return err
	}
	if matter.CreatorID != userID {
		return apperr.ErrForbidden
	}
	a := &model.MatterAssignee{
		MatterID: matterID,
		UserID:   assigneeUserID,
	}
	return s.assigneeRepo.Create(a)
}

func (s *MatterService) RemoveAssignee(matterID, spaceID, userID, assigneeUserID string) error {
	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return err
	}
	if matter.CreatorID != userID {
		return apperr.ErrForbidden
	}
	return s.assigneeRepo.Delete(matterID, assigneeUserID)
}
