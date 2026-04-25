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

// CreateGoal atomically inserts the goal and its owner membership row.
// Without the transaction a failed AddMember would leave a goal with no
// owner, which no other code path can fix (owner is required to add members).
func (s *GoalService) CreateGoal(spaceID, ownerID, title string, description *string) (*model.Goal, error) {
	goal := &model.Goal{
		SpaceID:     spaceID,
		Title:       title,
		Description: description,
		OwnerID:     ownerID,
	}
	err := s.tx.Do(func(r *repository.TxRepos) error {
		if err := r.Goal.Create(goal); err != nil {
			return err
		}
		return r.Goal.AddMember(goal.ID, ownerID, "owner")
	})
	if err != nil {
		return nil, err
	}
	return goal, nil
}

func (s *GoalService) ListGoals(spaceID string) ([]*model.Goal, error) {
	return s.goalRepo.ListBySpace(spaceID)
}

type GoalDetail struct {
	*model.Goal
	Members    []*model.GoalMember      `json:"members"`
	TodoCounts map[string]int           `json:"todo_counts"`
	Todos      map[string][]*model.Todo `json:"todos"`
}

// GetGoal returns the kanban detail for a goal. Access is restricted to goal
// members — listing goals in a Space does not imply the right to open any of
// them. The owner is implicitly a member (CreateGoal inserts the owner row in
// the same transaction), but we also allow owner-id match as a belt-and-braces
// check against any bug that drops the membership row.
func (s *GoalService) GetGoal(id, spaceID, userID string) (*GoalDetail, error) {
	goal, err := s.goalRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if goal.OwnerID != userID {
		isMember, err := s.goalRepo.IsMember(id, userID)
		if err != nil {
			return nil, err
		}
		if !isMember {
			return nil, apperr.Forbidden("not a member of this goal")
		}
	}
	members, err := s.goalRepo.ListMembers(id)
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
		Members:    members,
		TodoCounts: counts,
		Todos:      todos,
	}, nil
}

func (s *GoalService) UpdateGoal(id, spaceID, userID, title string, description *string) (*model.Goal, error) {
	goal, err := s.goalRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
	}
	if goal.OwnerID != userID {
		return nil, apperr.Forbidden("only the owner can update this goal")
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
	if goal.OwnerID != userID {
		return apperr.Forbidden("only the owner can archive this goal")
	}
	return s.goalRepo.Archive(id, spaceID)
}

func (s *GoalService) AddMember(goalID, spaceID, userID, memberID, role string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.OwnerID != userID {
		return apperr.Forbidden("only the owner can add members")
	}
	return s.goalRepo.AddMember(goalID, memberID, role)
}

func (s *GoalService) RemoveMember(goalID, spaceID, userID, memberID string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.OwnerID != userID {
		return apperr.Forbidden("only the owner can remove members")
	}
	if memberID == goal.OwnerID {
		return apperr.Forbidden("cannot remove the owner")
	}
	return s.goalRepo.RemoveMember(goalID, memberID)
}
