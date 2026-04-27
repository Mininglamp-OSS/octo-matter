package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// --- fake repos for unit tests ------------------------------------------

type fakeTodoRepo struct {
	todos map[string]*model.Todo
}

func newFakeTodoRepo(todos ...*model.Todo) *fakeTodoRepo {
	m := make(map[string]*model.Todo, len(todos))
	for _, t := range todos {
		m[t.ID] = t
	}
	return &fakeTodoRepo{todos: m}
}

func (f *fakeTodoRepo) Create(todo *model.Todo) error {
	if todo.ID == "" {
		todo.ID = "generated-" + todo.Title
	}
	f.todos[todo.ID] = todo
	return nil
}

func (f *fakeTodoRepo) GetByID(id, spaceID string) (*model.Todo, error) {
	t, ok := f.todos[id]
	if !ok || t.SpaceID != spaceID || t.DeletedAt != nil {
		return nil, apperr.ErrNotFound
	}
	return t, nil
}

func (f *fakeTodoRepo) ListBySpace(spaceID string, _ repository.TodoFilter) ([]*model.Todo, bool, error) {
	var out []*model.Todo
	for _, t := range f.todos {
		if t.SpaceID == spaceID && t.DeletedAt == nil {
			out = append(out, t)
		}
	}
	return out, false, nil
}

func (f *fakeTodoRepo) Update(todo *model.Todo) error {
	existing, ok := f.todos[todo.ID]
	if !ok || existing.SpaceID != todo.SpaceID {
		return apperr.ErrNotFound
	}
	f.todos[todo.ID] = todo
	return nil
}

func (f *fakeTodoRepo) UpdateStatus(id, spaceID, status string) error {
	t, err := f.GetByID(id, spaceID)
	if err != nil {
		return err
	}
	t.Status = model.TodoStatus(status)
	return nil
}

func (f *fakeTodoRepo) SoftDelete(id, spaceID string) error {
	t, err := f.GetByID(id, spaceID)
	if err != nil {
		return err
	}
	now := time.Now()
	t.DeletedAt = &now
	return nil
}

// --- assignee fake -------------------------------------------------------

type fakeAssigneeRepo struct {
	byTodo map[string][]*model.TodoAssignee
}

func newFakeAssigneeRepo() *fakeAssigneeRepo {
	return &fakeAssigneeRepo{byTodo: make(map[string][]*model.TodoAssignee)}
}

func (f *fakeAssigneeRepo) Create(a *model.TodoAssignee) error {
	f.byTodo[a.TodoID] = append(f.byTodo[a.TodoID], a)
	return nil
}

func (f *fakeAssigneeRepo) Delete(todoID, userID string) error {
	list := f.byTodo[todoID]
	kept := list[:0]
	for _, a := range list {
		if a.UserID != userID {
			kept = append(kept, a)
		}
	}
	f.byTodo[todoID] = kept
	return nil
}

func (f *fakeAssigneeRepo) UpdateStatus(todoID, userID, status string) error {
	for _, a := range f.byTodo[todoID] {
		if a.UserID == userID {
			a.Status = model.AssigneeStatus(status)
			return nil
		}
	}
	return apperr.ErrNotFound
}

func (f *fakeAssigneeRepo) MarkAllDone(todoID string) error {
	for _, a := range f.byTodo[todoID] {
		a.Status = model.AssigneeStatusDone
	}
	return nil
}

func (f *fakeAssigneeRepo) ListByTodo(todoID string) ([]*model.TodoAssignee, error) {
	return f.byTodo[todoID], nil
}

func (f *fakeAssigneeRepo) IsAssignee(todoID, userID string) (bool, error) {
	for _, a := range f.byTodo[todoID] {
		if a.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

// --- tx runner fake (noop — can't build real TxRepos) --------------------

type noopTxRunner struct{}

func (noopTxRunner) Do(fn func(r *repository.TxRepos) error) error {
	return fmt.Errorf("transaction exercised (test stub — requires integration test)")
}

// --- goal / access fakes ------------------------------------------------

type fakeGoalAccessChecker struct{}

func (fakeGoalAccessChecker) IsAssignee(goalID, userID string) (bool, error) { return false, nil }
func (fakeGoalAccessChecker) GetByID(id, spaceID string) (*model.Goal, error) {
	return nil, apperr.GoalNotFound()
}

type fakeAccessChecker struct{}

func (fakeAccessChecker) CanAccessTodo(todo *model.Todo, userID string) bool { return true }

// --- helpers -------------------------------------------------------------

func newTodoSvc(todoRepo todoStore, assigneeRepo assigneeStore) *TodoService {
	return NewTodoService(todoRepo, assigneeRepo, fakeGoalAccessChecker{}, noopTxRunner{})
}

// --- comment/attachment fakes -------------------------------------------

type fakeCommentRepo struct {
	byID map[string]*model.TodoComment
}

func newFakeCommentRepo(cs ...*model.TodoComment) *fakeCommentRepo {
	m := make(map[string]*model.TodoComment, len(cs))
	for _, c := range cs {
		m[c.ID] = c
	}
	return &fakeCommentRepo{byID: m}
}

func (f *fakeCommentRepo) Create(c *model.TodoComment) error {
	if c.ID == "" {
		c.ID = "c-generated"
	}
	f.byID[c.ID] = c
	return nil
}

func (f *fakeCommentRepo) GetByID(id string) (*model.TodoComment, error) {
	c, ok := f.byID[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return c, nil
}

func (f *fakeCommentRepo) Delete(id string) error { delete(f.byID, id); return nil }

func (f *fakeCommentRepo) ListByTodo(todoID string) ([]*model.TodoComment, error) {
	var out []*model.TodoComment
	for _, c := range f.byID {
		if c.TodoID == todoID {
			out = append(out, c)
		}
	}
	return out, nil
}

type fakeAttachmentRepo struct {
	byID map[string]*model.TodoAttachment
}

func newFakeAttachmentRepo(as ...*model.TodoAttachment) *fakeAttachmentRepo {
	m := make(map[string]*model.TodoAttachment, len(as))
	for _, a := range as {
		m[a.ID] = a
	}
	return &fakeAttachmentRepo{byID: m}
}

func (f *fakeAttachmentRepo) Create(a *model.TodoAttachment) error {
	if a.ID == "" {
		a.ID = "a-generated"
	}
	f.byID[a.ID] = a
	return nil
}

func (f *fakeAttachmentRepo) GetByID(id string) (*model.TodoAttachment, error) {
	a, ok := f.byID[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return a, nil
}

func (f *fakeAttachmentRepo) Delete(id string) error { delete(f.byID, id); return nil }

func (f *fakeAttachmentRepo) ListByTodo(todoID string) ([]*model.TodoAttachment, error) {
	var out []*model.TodoAttachment
	for _, a := range f.byID {
		if a.TodoID == todoID {
			out = append(out, a)
		}
	}
	return out, nil
}

// --- Tests ---------------------------------------------------------------

func TestTodoService_GetTodo_CrossSpaceReturnsNotFound(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.GetTodo("t1", "space-B", "caller")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space GetTodo: got %v, want ErrNotFound", err)
	}
}

func TestTodoService_UpdateTodo_CrossSpaceReturnsNotFound(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.UpdateTodo("t1", "space-B", "u1", "new title", nil, nil, nil, nil)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space UpdateTodo: got %v, want ErrNotFound", err)
	}
}

func TestTodoService_UpdateTodo_NonCreatorReturnsForbidden(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.UpdateTodo("t1", "space-A", "u2", "new", nil, nil, nil, nil)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-creator UpdateTodo: got %v, want ErrForbidden", err)
	}
}

func TestTodoService_SoftDelete_CrossSpaceReturnsNotFound(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	err := svc.SoftDelete("t1", "space-B", "u1")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space SoftDelete: got %v, want ErrNotFound", err)
	}
}

func TestCommentService_Create_CrossSpaceReturnsNotFound(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	svc := NewCommentService(newFakeCommentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	_, err := svc.CreateComment("t1", "space-B", "u2", "hi")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space CreateComment: got %v, want ErrNotFound", err)
	}
}

func TestCommentService_Delete_NonAuthorReturnsForbidden(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	comment := &model.TodoComment{ID: "c1", TodoID: "t1", UserID: "u1", Content: "hi"}
	svc := NewCommentService(newFakeCommentRepo(comment), newFakeTodoRepo(todo), fakeAccessChecker{})
	err := svc.DeleteComment("c1", "space-A", "u2")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-author DeleteComment: got %v, want ErrForbidden", err)
	}
}

func TestAttachmentService_Delete_NonUploaderReturnsForbidden(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	att := &model.TodoAttachment{ID: "a1", TodoID: "t1", UserID: "u1", FileURL: "http://x"}
	svc := NewAttachmentService(newFakeAttachmentRepo(att), newFakeTodoRepo(todo), fakeAccessChecker{})
	err := svc.DeleteAttachment("a1", "space-A", "u2")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-uploader DeleteAttachment: got %v, want ErrForbidden", err)
	}
}

func TestSetStatus_RejectsInvalidStatus(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.SetStatus("t1", "sp1", "u1", "banana")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("invalid status should return ErrInvalidInput, got %v", err)
	}
}
