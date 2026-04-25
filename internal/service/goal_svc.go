package service

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

type GoalService struct {
	goalRepo *repository.GoalRepo
	todoRepo *repository.TodoRepo
}

func NewGoalService(goalRepo *repository.GoalRepo, todoRepo *repository.TodoRepo) *GoalService {
	return &GoalService{goalRepo: goalRepo, todoRepo: todoRepo}
}

func (s *GoalService) CreateGoal(spaceID, ownerID, title string, description *string) (*model.Goal, error) {
	goal := &model.Goal{
		SpaceID:     spaceID,
		Title:       title,
		Description: description,
		OwnerID:     ownerID,
	}
	if err := s.goalRepo.Create(goal); err != nil {
		return nil, err
	}
	if err := s.goalRepo.AddMember(goal.ID, ownerID, "owner"); err != nil {
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

func (s *GoalService) GetGoal(id, spaceID string) (*GoalDetail, error) {
	goal, err := s.goalRepo.GetByID(id, spaceID)
	if err != nil {
		return nil, err
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
		return nil, apperr.ErrForbidden
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
		return apperr.ErrForbidden
	}
	return s.goalRepo.Archive(id, spaceID)
}

func (s *GoalService) AddMember(goalID, spaceID, userID, memberID, role string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.OwnerID != userID {
		return apperr.ErrForbidden
	}
	return s.goalRepo.AddMember(goalID, memberID, role)
}

func (s *GoalService) RemoveMember(goalID, spaceID, userID, memberID string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.OwnerID != userID {
		return apperr.ErrForbidden
	}
	if memberID == goal.OwnerID {
		return apperr.ErrForbidden
	}
	return s.goalRepo.RemoveMember(goalID, memberID)
}
