package service

import (
	"log"
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
	HasAccess(matterID string, callerUIDs []string, channelID string) (bool, error)
}

type assigneeStore interface {
	Create(a *model.MatterAssignee) error
	Delete(matterID, userID string) error
	ListByMatter(matterID string) ([]*model.MatterAssignee, error)
	IsAssignee(matterID, userID string) (bool, error)
	IsAssigneeAny(matterID string, userIDs []string) (bool, error)
}

type participantStore interface {
	Upsert(matterID, userID string) error
	IsParticipantAny(matterID string, userIDs []string) (bool, error)
	ListUserIDs(matterID string) ([]string, error)
}

type channelStore interface {
	Create(mc *model.MatterChannel) error
	Delete(matterID, channelID string) error
	IsLinkedChannel(matterID, channelID string) (bool, error)
	ListByMatter(matterID string) ([]*model.MatterChannel, error)
}

// txRunner runs a closure against a bundle of tx-bound repos.
type txRunner interface {
	Do(fn func(r *repository.TxRepos) error) error
}

type MatterService struct {
	matterRepo      matterStore
	assigneeRepo    assigneeStore
	participantRepo participantStore
	channelRepo     channelStore
	tx              txRunner
}

func NewMatterService(
	matterRepo matterStore,
	assigneeRepo assigneeStore,
	participantRepo participantStore,
	channelRepo channelStore,
	tx txRunner,
) *MatterService {
	return &MatterService{
		matterRepo:      matterRepo,
		assigneeRepo:    assigneeRepo,
		participantRepo: participantRepo,
		channelRepo:     channelRepo,
		tx:              tx,
	}
}

// CreateMatterWithAssignees creates the matter, auto-links its source channel
// (GAP #13) and inserts all initial assignees in a single transaction.
func (s *MatterService) CreateMatterWithAssignees(matter *model.Matter, assigneeIDs []string) (*MatterDetail, error) {
	if matter.Status == "" {
		matter.Status = model.MatterStatusOpen
	}
	var created []*model.MatterAssignee
	err := s.tx.Do(func(r *repository.TxRepos) error {
		if err := r.Matter.Create(matter); err != nil {
			return err
		}

		// GAP #13: auto-link the originating channel so later members can still access it.
		if matter.SourceChannelID != nil && *matter.SourceChannelID != "" {
			chType := uint8(0)
			if matter.SourceChannelType != nil {
				chType = *matter.SourceChannelType
			}
			mc := &model.MatterChannel{
				MatterID:    matter.ID,
				ChannelID:   *matter.SourceChannelID,
				ChannelType: chType,
				ChannelName: matter.SourceName,
				LinkedBy:    matter.CreatorID,
			}
			if err := r.MatterChannel.Create(mc); err != nil {
				return err
			}
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
	Assignees    []*model.MatterAssignee  `json:"assignees"`
	Participants []string                 `json:"participants"`
	Channels     []*model.MatterChannel   `json:"channels"`
}

// GetMatterForNotification loads a matter by ID+space without access checks.
func (s *MatterService) GetMatterForNotification(id, spaceID string) (*model.Matter, error) {
	return s.matterRepo.GetByID(id, spaceID)
}

// GetMatter loads a matter any of callerUIDs is allowed to read.
func (s *MatterService) GetMatter(id, spaceID string, callerUIDs []string, sourceChannelID string) (*MatterDetail, error) {
	matter, err := s.matterRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	ok, accessErr := s.canAccessMatter(matter, callerUIDs, sourceChannelID)
	if accessErr != nil {
		return nil, accessErr
	}
	if !ok {
		return nil, apperr.Forbidden("not authorized to view this matter")
	}
	assignees, err := s.assigneeRepo.ListByMatter(id)
	if err != nil {
		return nil, err
	}
	participants, err := s.participantRepo.ListUserIDs(id)
	if err != nil {
		return nil, err
	}
	channels, err := s.channelRepo.ListByMatter(id)
	if err != nil {
		return nil, err
	}
	return &MatterDetail{
		Matter:       matter,
		Assignees:    assignees,
		Participants: participants,
		Channels:     channels,
	}, nil
}

// CanAccessMatter satisfies MatterAccessChecker.
func (s *MatterService) CanAccessMatter(matter *model.Matter, callerUIDs []string, sourceChannelID string) (bool, error) {
	return s.canAccessMatter(matter, callerUIDs, sourceChannelID)
}

// canAccessMatter returns (true, nil) if access is granted, (false, nil) if
// denied, or (false, err) on infrastructure failure (DB error).
func (s *MatterService) canAccessMatter(matter *model.Matter, callerUIDs []string, sourceChannelID string) (bool, error) {
	// Fast path: creator check is in-memory, no DB needed.
	if s.isCreator(matter, callerUIDs) {
		return true, nil
	}
	// Single DB query to check assignee, participant, or channel access.
	ok, err := s.matterRepo.HasAccess(matter.ID, callerUIDs, sourceChannelID)
	if err != nil {
		log.Printf("[ERROR] canAccessMatter: HasAccess DB error matter=%s: %v", matter.ID, err)
		return false, err
	}
	return ok, nil
}

func (s *MatterService) isCreator(matter *model.Matter, callerUIDs []string) bool {
	for _, uid := range callerUIDs {
		if matter.CreatorID == uid {
			return true
		}
	}
	return false
}

func (s *MatterService) isAssigneeAny(matterID string, callerUIDs []string) bool {
	ok, _ := s.assigneeRepo.IsAssigneeAny(matterID, callerUIDs)
	return ok
}

// UpdateMatter applies editable fields. Creator or any assignee (expanded via
// callerUIDs) may edit.
func (s *MatterService) UpdateMatter(id, spaceID string, callerUIDs []string, title *string, description *string, deadline, remindAt *string) (*model.Matter, error) {
	matter, err := s.matterRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.isCreator(matter, callerUIDs) && !s.isAssigneeAny(id, callerUIDs) {
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

// SetStatus changes a matter's status. Archive transitions (either direction)
// require Creator; open↔done is allowed for Creator or any assignee.
func (s *MatterService) SetStatus(id, spaceID string, callerUIDs []string, target model.MatterStatus) (*MatterDetail, error) {
	if !model.IsValidStatus(target) {
		return nil, apperr.InvalidInput("status must be 'open', 'done', or 'archived'")
	}

	// Pre-check: if target is archived, only creator can do it.
	// (Full check including current-status-is-archived runs inside tx with row lock.)
	if target == model.MatterStatusArchived {
		matter, err := s.matterRepo.GetByID(id, spaceID)
		if err != nil {
			return nil, err
		}
		if !s.isCreator(matter, callerUIDs) {
			return nil, apperr.Forbidden("only creator can archive/unarchive")
		}
	}

	err := s.tx.Do(func(r *repository.TxRepos) error {
		matter, err := r.Matter.GetByIDForUpdate(id, spaceID)
		if err != nil {
			return err
		}

		isCreator := s.isCreator(matter, callerUIDs)
		isAssigneeAny, _ := r.Assignee.IsAssigneeAny(id, callerUIDs)

		involvesArchived := target == model.MatterStatusArchived ||
			matter.Status == model.MatterStatusArchived
		if involvesArchived {
			if !isCreator {
				return apperr.Forbidden("only creator can archive/unarchive")
			}
		} else {
			if !isCreator && !isAssigneeAny {
				return apperr.Forbidden("only creator or assignee can change status")
			}
		}

		if matter.Status == target {
			return nil
		}

		if matter.Status == model.MatterStatusArchived && target == model.MatterStatusDone {
			return apperr.InvalidInput("cannot transition from archived to done; reopen first")
		}

		return r.Matter.UpdateStatus(id, spaceID, string(target))
	})
	if err != nil {
		return nil, err
	}

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

func (s *MatterService) SoftDelete(id, spaceID string, callerUIDs []string) error {
	matter, err := s.matterRepo.GetByID(id, spaceID)
	if err != nil {
		return err
	}
	if !s.isCreator(matter, callerUIDs) {
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

// ListParticipantIDs returns every participant uid for a matter.
func (s *MatterService) ListParticipantIDs(matterID string) ([]string, error) {
	return s.participantRepo.ListUserIDs(matterID)
}

func (s *MatterService) AddAssignee(matterID, spaceID string, callerUIDs []string, assigneeUserID string) error {
	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return err
	}
	if !s.isCreator(matter, callerUIDs) && !s.isAssigneeAny(matterID, callerUIDs) {
		return apperr.ErrForbidden
	}
	return s.assigneeRepo.Create(&model.MatterAssignee{
		MatterID: matterID,
		UserID:   assigneeUserID,
	})
}

func (s *MatterService) RemoveAssignee(matterID, spaceID string, callerUIDs []string, assigneeUserID string) error {
	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return err
	}

	isSelfUnassign := false
	for _, uid := range callerUIDs {
		if uid == assigneeUserID {
			isSelfUnassign = true
			break
		}
	}
	if !isSelfUnassign && !s.isCreator(matter, callerUIDs) {
		return apperr.ErrForbidden
	}
	return s.assigneeRepo.Delete(matterID, assigneeUserID)
}

// LinkChannel attaches a channel to a matter. Creator or any assignee may link.
func (s *MatterService) LinkChannel(matterID, spaceID string, callerUIDs []string, channelID string, channelType uint8, channelName *string) (*model.MatterChannel, error) {
	if len(callerUIDs) == 0 {
		return nil, apperr.InvalidInput("caller identity required")
	}
	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.isCreator(matter, callerUIDs) && !s.isAssigneeAny(matterID, callerUIDs) {
		return nil, apperr.ErrForbidden
	}
	mc := &model.MatterChannel{
		MatterID:    matterID,
		ChannelID:   channelID,
		ChannelType: channelType,
		ChannelName: channelName,
		LinkedBy:    callerUIDs[0],
	}
	if err := s.channelRepo.Create(mc); err != nil {
		return nil, err
	}
	return mc, nil
}

// UnlinkChannel removes a channel link. Only Creator may unlink.
func (s *MatterService) UnlinkChannel(matterID, spaceID string, callerUIDs []string, channelID string) error {
	matter, err := s.matterRepo.GetByID(matterID, spaceID)
	if err != nil {
		return err
	}
	if !s.isCreator(matter, callerUIDs) {
		return apperr.ErrForbidden
	}
	return s.channelRepo.Delete(matterID, channelID)
}
