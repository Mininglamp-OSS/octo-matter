package service

import (
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

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
	GetTodoStatsByGoals(goalIDs []string) (map[string]*repository.TodoStats, error)
}

type GoalService struct {
	goalRepo goalStore
	tx       txRunner
}

func NewGoalService(goalRepo goalStore, tx txRunner) *GoalService {
	return &GoalService{goalRepo: goalRepo, tx: tx}
}

func (s *GoalService) CreateGoal(spaceID, creatorID, title string, description *string, deadline *string, assigneeIDs []string) (*model.Goal, error) {
	goal := &model.Goal{
		SpaceID:     spaceID,
		Title:       title,
		Description: description,
		CreatorID:   creatorID,
		Status:      model.GoalStatusActive,
	}
	if deadline != nil {
		t, err := ParseOptionalRFC3339(*deadline)
		if err != nil {
			return nil, apperr.InvalidInput("deadline must be RFC3339 or empty")
		}
		goal.Deadline = t
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

type GoalWithStats struct {
	*model.Goal
	OpenCount   int `json:"open_count"`
	ClosedCount int `json:"closed_count"`
}

type GoalListResult struct {
	Items      []*GoalWithStats `json:"items"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func (s *GoalService) ListGoals(spaceID string, filter repository.GoalFilter) (*GoalListResult, error) {
	goals, hasMore, err := s.goalRepo.ListByUser(spaceID, filter)
	if err != nil {
		return nil, err
	}
	var nextCursor string
	if hasMore && len(goals) > 0 {
		last := goals[len(goals)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}

	goalIDs := make([]string, 0, len(goals))
	for _, g := range goals {
		goalIDs = append(goalIDs, g.ID)
	}
	statsMap, _ := s.goalRepo.GetTodoStatsByGoals(goalIDs)

	items := make([]*GoalWithStats, 0, len(goals))
	for _, g := range goals {
		gs := &GoalWithStats{Goal: g}
		if stats, ok := statsMap[g.ID]; ok {
			gs.OpenCount = stats.OpenCount
			gs.ClosedCount = stats.ClosedCount
		}
		items = append(items, gs)
	}
	return &GoalListResult{Items: items, HasMore: hasMore, NextCursor: nextCursor}, nil
}

type GoalDetail struct {
	*model.Goal
	Assignees   []*model.GoalAssignee `json:"assignees"`
	OpenCount   int                   `json:"open_count"`
	ClosedCount int                   `json:"closed_count"`
}

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
	statsMap, _ := s.goalRepo.GetTodoStatsByGoals([]string{id})
	detail := &GoalDetail{Goal: goal, Assignees: assignees}
	if stats, ok := statsMap[id]; ok {
		detail.OpenCount = stats.OpenCount
		detail.ClosedCount = stats.ClosedCount
	}
	return detail, nil
}

func (s *GoalService) UpdateGoal(id, spaceID, userID, title string, description *string, deadline *string) (*model.Goal, error) {
	goal, err := s.goalRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	// Creator or assignee can update a goal.
	isCreator := goal.CreatorID == userID
	isAssignee, _ := s.goalRepo.IsAssignee(id, userID)
	if !isCreator && !isAssignee {
		return nil, apperr.Forbidden("only the creator or assignee can update this goal")
	}
	goal.Title = title
	goal.Description = description
	if deadline != nil {
		t, err := ParseOptionalRFC3339(*deadline)
		if err != nil {
			return nil, apperr.InvalidInput("deadline must be RFC3339 or empty")
		}
		goal.Deadline = t
	}
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

var _ = time.RFC3339
