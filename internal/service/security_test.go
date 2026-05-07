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

// --- tx runner fakes -----------------------------------------------------

// noopTxRunner fails any Do() call. Used by TodoService tests that never
// reach a transactional branch.
type noopTxRunner struct{}

func (noopTxRunner) Do(fn func(r *repository.TxRepos) error) error {
	return fmt.Errorf("transaction exercised (test stub — requires integration test)")
}

// fakeCommentTx runs the closure directly against the supplied fake repos.
// Every Do() call executes synchronously — there is no rollback or real
// transaction. Mutations made inside the closure persist on the fakes.
type fakeCommentTx struct {
	comments    CommentStore
	attachments CommentAttachmentStore
}

func (f fakeCommentTx) Do(fn func(CommentStore, CommentAttachmentStore) error) error {
	return fn(f.comments, f.attachments)
}

// --- goal / access fakes ------------------------------------------------

type fakeGoalAccessChecker struct{}

func (fakeGoalAccessChecker) IsAssignee(goalID, userID string) (bool, error) { return false, nil }
func (fakeGoalAccessChecker) GetByID(id, spaceID string) (*model.Goal, error) {
	return nil, apperr.GoalNotFound()
}

type fakeAccessChecker struct{}

func (fakeAccessChecker) CanAccessTodo(todo *model.Todo, userID string, sourceChannelID string) bool {
	return true
}

// --- helpers -------------------------------------------------------------

func newTodoSvc(todoRepo todoStore, assigneeRepo assigneeStore) *TodoService {
	return NewTodoService(todoRepo, assigneeRepo, fakeGoalAccessChecker{}, noopTxRunner{})
}

func newCommentSvc(
	commentRepo *fakeCommentRepo,
	attachmentRepo *fakeCommentAttachmentRepo,
	todoRepo *fakeTodoRepo,
	access TodoAccessChecker,
) *CommentService {
	return NewCommentService(
		commentRepo,
		attachmentRepo,
		todoRepo,
		access,
		fakeCommentTx{comments: commentRepo, attachments: attachmentRepo},
	)
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
		c.ID = fmt.Sprintf("c-%d", len(f.byID)+1)
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

func (f *fakeCommentRepo) ListByTodo(todoID string, cursor *string, limit int) ([]*model.TodoComment, bool, error) {
	var out []*model.TodoComment
	for _, c := range f.byID {
		if c.TodoID == todoID {
			out = append(out, c)
		}
	}
	return out, false, nil
}

type fakeCommentAttachmentRepo struct {
	byCommentID map[string][]*model.CommentAttachment
}

func newFakeCommentAttachmentRepo() *fakeCommentAttachmentRepo {
	return &fakeCommentAttachmentRepo{byCommentID: make(map[string][]*model.CommentAttachment)}
}

func (f *fakeCommentAttachmentRepo) CreateMany(atts []*model.CommentAttachment) error {
	for i, a := range atts {
		if a.ID == "" {
			a.ID = fmt.Sprintf("a-%d-%d", len(f.byCommentID), i)
		}
		f.byCommentID[a.CommentID] = append(f.byCommentID[a.CommentID], a)
	}
	return nil
}

func (f *fakeCommentAttachmentRepo) ListByCommentIDs(ids []string) (map[string][]model.CommentAttachment, error) {
	out := make(map[string][]model.CommentAttachment, len(ids))
	for _, id := range ids {
		for _, a := range f.byCommentID[id] {
			out[id] = append(out[id], *a)
		}
	}
	return out, nil
}

func (f *fakeCommentAttachmentRepo) DeleteByCommentID(commentID string) error {
	delete(f.byCommentID, commentID)
	return nil
}

// --- Tests ---------------------------------------------------------------

func TestTodoService_GetTodo_CrossSpaceReturnsNotFound(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.GetTodo("t1", "space-B", "caller", "")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space GetTodo: got %v, want ErrNotFound", err)
	}
}

func TestTodoService_UpdateTodo_CrossSpaceReturnsNotFound(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.UpdateTodo("t1", "space-B", "u1", strPtr("new title"), nil, nil, nil, nil)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space UpdateTodo: got %v, want ErrNotFound", err)
	}
}

func TestTodoService_UpdateTodo_NonCreatorReturnsForbidden(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.TodoStatusOpen}
	svc := newTodoSvc(newFakeTodoRepo(todo), newFakeAssigneeRepo())
	_, err := svc.UpdateTodo("t1", "space-A", "u2", strPtr("new"), nil, nil, nil, nil)
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
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	_, err := svc.CreateComment("t1", "space-B", "u2", "hi", nil, "")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space CreateComment: got %v, want ErrNotFound", err)
	}
}

func TestCommentService_Create_EmptyRejected(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	_, err := svc.CreateComment("t1", "sp1", "u1", "", nil, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("empty comment should be invalid, got %v", err)
	}
}

func TestCommentService_Create_AttachmentsOnly_OK(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	c, err := svc.CreateComment("t1", "sp1", "u1", "", []CommentAttachmentInput{
		{FileURL: "https://obj/a.png"},
	}, "")
	if err != nil {
		t.Fatalf("attachment-only create: %v", err)
	}
	if c.Content != nil {
		t.Fatalf("content should be nil, got %v", *c.Content)
	}
	if len(c.Attachments) != 1 || c.Attachments[0].FileURL != "https://obj/a.png" {
		t.Fatalf("attachments mismatch: %#v", c.Attachments)
	}
}

func TestCommentService_Create_TooManyAttachments(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	atts := make([]CommentAttachmentInput, MaxAttachmentsPerComment+1)
	for i := range atts {
		atts[i] = CommentAttachmentInput{FileURL: "https://obj/a.png"}
	}
	_, err := svc.CreateComment("t1", "sp1", "u1", "hi", atts, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("too-many attachments should be invalid, got %v", err)
	}
}

func TestCommentService_Create_AttachmentTooLarge(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	over := int64(MaxAttachmentSizeBytes + 1)
	_, err := svc.CreateComment("t1", "sp1", "u1", "", []CommentAttachmentInput{
		{FileURL: "https://obj/a.png", FileSize: &over},
	}, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("oversize attachment should be invalid, got %v", err)
	}
}

func TestCommentService_Create_WithTextAndAttachments_OK(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	c, err := svc.CreateComment("t1", "sp1", "u1", "look at this", []CommentAttachmentInput{
		{FileURL: "https://obj/a.png"},
		{FileURL: "https://obj/b.png"},
	}, "")
	if err != nil {
		t.Fatalf("create with text+attachments: %v", err)
	}
	if c.Content == nil || *c.Content != "look at this" {
		t.Fatalf("content mismatch: %v", c.Content)
	}
	if len(c.Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(c.Attachments))
	}
}

func TestCommentService_Delete_NonAuthorReturnsForbidden(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	content := "hi"
	comment := &model.TodoComment{ID: "c1", TodoID: "t1", UserID: "u1", Content: &content}
	svc := newCommentSvc(newFakeCommentRepo(comment), newFakeCommentAttachmentRepo(), newFakeTodoRepo(todo), fakeAccessChecker{})
	err := svc.DeleteComment("c1", "space-A", "u2", "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-author DeleteComment: got %v, want ErrForbidden", err)
	}
}

func TestCommentService_Delete_AuthorRemovesAttachments(t *testing.T) {
	todo := &model.Todo{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	content := "hi"
	comment := &model.TodoComment{ID: "c1", TodoID: "t1", UserID: "u1", Content: &content}
	cmtRepo := newFakeCommentRepo(comment)
	attRepo := newFakeCommentAttachmentRepo()
	attRepo.byCommentID["c1"] = []*model.CommentAttachment{
		{ID: "a1", CommentID: "c1", FileURL: "https://obj/a.png"},
	}
	svc := newCommentSvc(cmtRepo, attRepo, newFakeTodoRepo(todo), fakeAccessChecker{})
	if err := svc.DeleteComment("c1", "sp1", "u1", ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := cmtRepo.byID["c1"]; ok {
		t.Fatalf("comment not removed")
	}
	if _, ok := attRepo.byCommentID["c1"]; ok {
		t.Fatalf("attachments not removed")
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

func strPtr(s string) *string { return &s }
