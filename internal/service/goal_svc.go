package service

import (
	"errors"

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
	// add owner as admin member
	_ = s.goalRepo.AddMember(goal.ID, ownerID, "admin")
	return goal, nil
}

func (s *GoalService) ListGoals(spaceID string) ([]*model.Goal, error) {
	return s.goalRepo.ListBySpace(spaceID)
}

type GoalDetail struct {
	*model.Goal
	Members    []*model.GoalMember    `json:"members"`
	TodoCounts map[string]int         `json:"todo_counts"`
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
	counts, err := s.todoRepo.CountByGoalStatus(id)
	if err != nil {
		return nil, err
	}
	todos, err := s.todoRepo.ListByGoalGroupedByStatus(id)
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
		return nil, errors.New("only the owner can update a goal")
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
		return errors.New("only the owner can archive a goal")
	}
	return s.goalRepo.Archive(id, spaceID)
}

func (s *GoalService) AddMember(goalID, spaceID, userID, memberID, role string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.OwnerID != userID {
		return errors.New("only the owner can add members")
	}
	return s.goalRepo.AddMember(goalID, memberID, role)
}

func (s *GoalService) RemoveMember(goalID, spaceID, userID, memberID string) error {
	goal, err := s.goalRepo.GetByID(goalID, spaceID)
	if err != nil {
		return err
	}
	if goal.OwnerID != userID {
		return errors.New("only the owner can remove members")
	}
	if memberID == goal.OwnerID {
		return errors.New("cannot remove the owner from the goal")
	}
	return s.goalRepo.RemoveMember(goalID, memberID)
}
