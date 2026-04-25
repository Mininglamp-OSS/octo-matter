package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

type GoalService struct {
	goalRepo *repository.GoalRepo
	todoRepo *repository.TodoRepo
	tx       txRunner
}

func NewGoalService(goalRepo *repository.GoalRepo, todoRepo *repository.TodoRepo, tx txRunner) *GoalService {
	return &GoalService{goalRepo: goalRepo, todoRepo: todoRepo, tx: tx}
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

// ListGoals returns goals where the user is creator or assignee.
func (s *GoalService) ListGoals(spaceID, userID string) ([]*model.Goal, error) {
	return s.goalRepo.ListByUser(spaceID, userID)
}

type GoalDetail struct {
	*model.Goal
	Assignees  []*model.GoalAssignee    `json:"assignees"`
	TodoCounts map[string]int           `json:"todo_counts"`
	Todos      map[string][]*model.Todo `json:"todos"`
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
	counts, err := s.todoRepo.CountByGoalStatus(spaceID, id)
	if err != nil {
		return nil, err
	}
	todos, err := s.todoRepo.ListByGoalGroupedByStatus(spaceID, id)
	if err != nil {
		return nil, err
	}
	return &GoalDetail{
		Goal:       goal,
		Assignees:  assignees,
		TodoCounts: counts,
		Todos:      todos,
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

// canAccessGoal checks if user is creator or assignee of the goal.
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
