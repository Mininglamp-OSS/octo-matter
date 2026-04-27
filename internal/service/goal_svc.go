package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// goalStore is the subset of GoalRepo the service depends on.
type goalStore interface {
	Create(goal *model.Goal) error
	GetByID(id, spaceID string) (*model.Goal, error)
	ListByUser(spaceID string, filter repository.GoalFilter) ([]*model.Goal, bool, error)
	Update(goal *model.Goal) error
	Archive(id, spaceID string) error
	AddAssignee(goalID, userID string) error
	RemoveAssignee(goalID, userID string) error
	ListAssignees(goalID string) ([]*model.GoalAssignee, error)
	IsAssignee(goalID, userID string) (bool, error)
}

type GoalService struct {
	goalRepo goalStore
	tx       txRunner
}

func NewGoalService(goalRepo goalStore, tx txRunner) *GoalService {
	return &GoalService{goalRepo: goalRepo, tx: tx}
}

func (s *GoalService) CreateGoal(spaceID, creatorID, title string, description *string, assigneeIDs []string) (*model.Goal, error) {
	goal := &model.Goal{
		SpaceID:     spaceID,
		Title:       title,
		Description: description,
		CreatorID:   creatorID,
	}
	err := s.tx.Do(func(r *repository.TxRepos) error {
		if err := r.Goal.Create(goal); err != nil {
			return err
		}
		for _, uid := range assigneeIDs {
			if err := r.Goal.AddAssignee(goal.ID, uid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return goal, nil
}

// GoalListResult wraps paginated goal results.
type GoalListResult struct {
	Items      []*model.Goal `json:"items"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// ListGoals returns goals where the user is creator or assignee, with cursor pagination.
func (s *GoalService) ListGoals(spaceID string, filter repository.GoalFilter) (*GoalListResult, error) {
	goals, hasMore, err := s.goalRepo.ListByUser(spaceID, filter)
	if err != nil {
		return nil, err
	}
	var nextCursor string
	if hasMore && len(goals) > 0 {
		last := goals[len(goals)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	return &GoalListResult{
		Items:      goals,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// GoalDetail is the enriched response for a single goal (no kanban — just
// metadata + assignees).
type GoalDetail struct {
	*model.Goal
	Assignees []*model.GoalAssignee `json:"assignees"`
}

// GetGoal returns goal detail. Only creator or assignee can access.
func (s *GoalService) GetGoal(id, spaceID, userID string) (*GoalDetail, error) {
	goal, err := s.goalRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if !s.canAccessGoal(goal, userID) {
		return nil, apperr.Forbidden("not a creator or assignee of this goal")
	}
	assignees, err := s.goalRepo.ListAssignees(id)
	if err != nil {
		return nil, err
	}
	return &GoalDetail{
		Goal:      goal,
		Assignees: assignees,
	}, nil
}

func (s *GoalService) UpdateGoal(id, spaceID, userID, title string, description *string) (*model.Goal, error) {
	goal, err := s.goalRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if goal.CreatorID != userID {
		return nil, apperr.Forbidden("only the creator can update this goal")
	}
	goal.Title = title
	goal.Description = description
	if err := s.goalRepo.Update(goal); err != nil {
		return nil, err
	}
	return goal, nil
}

func (s *GoalService) ArchiveGoal(id, spaceID, userID string) error {
	goal, err := s.goalRepo.GetByID(id, spaceID)
	if err != nil {
		return err
	}
	if goal.CreatorID != userID {
		return apperr.Forbidden("only the creator can archive this goal")
	}
	return s.goalRepo.Archive(id, spaceID)
}

func (s *GoalService) AddAssignee(goalID, spaceID, userID, targetUID string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.CreatorID != userID {
		return apperr.Forbidden("only the creator can add assignees")
	}
	return s.goalRepo.AddAssignee(goalID, targetUID)
}

func (s *GoalService) RemoveAssignee(goalID, spaceID, userID, targetUID string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.CreatorID != userID {
		return apperr.Forbidden("only the creator can remove assignees")
	}
	return s.goalRepo.RemoveAssignee(goalID, targetUID)
}

func (s *GoalService) canAccessGoal(goal *model.Goal, userID string) bool {
	if goal.CreatorID == userID {
		return true
	}
	isAssignee, err := s.goalRepo.IsAssignee(goal.ID, userID)
	if err != nil {
		return false
	}
	return isAssignee
}
