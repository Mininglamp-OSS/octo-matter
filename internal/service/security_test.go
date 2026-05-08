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

type fakeMatterRepo struct {
	matters map[string]*model.Matter
}

func newFakeMatterRepo(matters ...*model.Matter) *fakeMatterRepo {
	m := make(map[string]*model.Matter, len(matters))
	for _, t := range matters {
		m[t.ID] = t
	}
	return &fakeMatterRepo{matters: m}
}

func (f *fakeMatterRepo) Create(matter *model.Matter) error {
	if matter.ID == "" {
		matter.ID = "generated-" + matter.Title
	}
	f.matters[matter.ID] = matter
	return nil
}

func (f *fakeMatterRepo) GetByID(id, spaceID string) (*model.Matter, error) {
	t, ok := f.matters[id]
	if !ok || t.SpaceID != spaceID || t.DeletedAt != nil {
		return nil, apperr.ErrNotFound
	}
	return t, nil
}

func (f *fakeMatterRepo) ListBySpace(spaceID string, _ repository.MatterFilter) ([]*model.Matter, bool, error) {
	var out []*model.Matter
	for _, t := range f.matters {
		if t.SpaceID == spaceID && t.DeletedAt == nil {
			out = append(out, t)
		}
	}
	return out, false, nil
}

func (f *fakeMatterRepo) Update(matter *model.Matter) error {
	existing, ok := f.matters[matter.ID]
	if !ok || existing.SpaceID != matter.SpaceID {
		return apperr.ErrNotFound
	}
	f.matters[matter.ID] = matter
	return nil
}

func (f *fakeMatterRepo) UpdateStatus(id, spaceID, status string) error {
	t, err := f.GetByID(id, spaceID)
	if err != nil {
		return err
	}
	t.Status = model.MatterStatus(status)
	return nil
}

func (f *fakeMatterRepo) SoftDelete(id, spaceID string) error {
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
	byMatter map[string][]*model.MatterAssignee
}

func newFakeAssigneeRepo() *fakeAssigneeRepo {
	return &fakeAssigneeRepo{byMatter: make(map[string][]*model.MatterAssignee)}
}

func (f *fakeAssigneeRepo) Create(a *model.MatterAssignee) error {
	f.byMatter[a.MatterID] = append(f.byMatter[a.MatterID], a)
	return nil
}

func (f *fakeAssigneeRepo) Delete(matterID, userID string) error {
	list := f.byMatter[matterID]
	kept := list[:0]
	for _, a := range list {
		if a.UserID != userID {
			kept = append(kept, a)
		}
	}
	f.byMatter[matterID] = kept
	return nil
}

func (f *fakeAssigneeRepo) ListByMatter(matterID string) ([]*model.MatterAssignee, error) {
	return f.byMatter[matterID], nil
}

func (f *fakeAssigneeRepo) IsAssignee(matterID, userID string) (bool, error) {
	for _, a := range f.byMatter[matterID] {
		if a.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

// --- tx runner fakes -----------------------------------------------------

// noopTxRunner fails any Do() call.
type noopTxRunner struct{}

func (noopTxRunner) Do(fn func(r *repository.TxRepos) error) error {
	return fmt.Errorf("transaction exercised (test stub — requires integration test)")
}

// fakeCommentTx runs the closure directly against the supplied fake repos.
type fakeCommentTx struct {
	comments    CommentStore
	attachments CommentAttachmentStore
}

func (f fakeCommentTx) Do(fn func(CommentStore, CommentAttachmentStore) error) error {
	return fn(f.comments, f.attachments)
}

// --- access fakes --------------------------------------------------------

type fakeAccessChecker struct{}

func (fakeAccessChecker) CanAccessMatter(matter *model.Matter, userID string, sourceChannelID string) bool {
	return true
}

// --- helpers -------------------------------------------------------------

func newMatterSvc(matterRepo matterStore, assigneeRepo assigneeStore) *MatterService {
	return NewMatterService(matterRepo, assigneeRepo, noopTxRunner{})
}

func newCommentSvc(
	commentRepo *fakeCommentRepo,
	attachmentRepo *fakeCommentAttachmentRepo,
	matterRepo *fakeMatterRepo,
	access MatterAccessChecker,
) *CommentService {
	return NewCommentService(
		commentRepo,
		attachmentRepo,
		matterRepo,
		access,
		fakeCommentTx{comments: commentRepo, attachments: attachmentRepo},
	)
}

// --- comment/attachment fakes -------------------------------------------

type fakeCommentRepo struct {
	byID map[string]*model.MatterComment
}

func newFakeCommentRepo(cs ...*model.MatterComment) *fakeCommentRepo {
	m := make(map[string]*model.MatterComment, len(cs))
	for _, c := range cs {
		m[c.ID] = c
	}
	return &fakeCommentRepo{byID: m}
}

func (f *fakeCommentRepo) Create(c *model.MatterComment) error {
	if c.ID == "" {
		c.ID = fmt.Sprintf("c-%d", len(f.byID)+1)
	}
	f.byID[c.ID] = c
	return nil
}

func (f *fakeCommentRepo) GetByID(id string) (*model.MatterComment, error) {
	c, ok := f.byID[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return c, nil
}

func (f *fakeCommentRepo) Delete(id string) error { delete(f.byID, id); return nil }

func (f *fakeCommentRepo) ListByMatter(matterID string, cursor *string, limit int) ([]*model.MatterComment, bool, error) {
	var out []*model.MatterComment
	for _, c := range f.byID {
		if c.MatterID == matterID {
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

func TestMatterService_GetMatter_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.GetMatter("t1", "space-B", "caller", "")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space GetMatter: got %v, want ErrNotFound", err)
	}
}

func TestMatterService_UpdateMatter_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.UpdateMatter("t1", "space-B", "u1", strPtr("new title"), nil, nil, nil)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space UpdateMatter: got %v, want ErrNotFound", err)
	}
}

func TestMatterService_UpdateMatter_NonCreatorReturnsForbidden(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.UpdateMatter("t1", "space-A", "u2", strPtr("new"), nil, nil, nil)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-creator UpdateMatter: got %v, want ErrForbidden", err)
	}
}

func TestMatterService_SoftDelete_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	err := svc.SoftDelete("t1", "space-B", "u1")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space SoftDelete: got %v, want ErrNotFound", err)
	}
}

func TestCommentService_Create_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	_, err := svc.CreateComment("t1", "space-B", "u2", "hi", nil, "")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space CreateComment: got %v, want ErrNotFound", err)
	}
}

func TestCommentService_Create_EmptyRejected(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	_, err := svc.CreateComment("t1", "sp1", "u1", "", nil, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("empty comment should be invalid, got %v", err)
	}
}

func TestCommentService_Create_AttachmentsOnly_OK(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
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
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
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
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	over := int64(MaxAttachmentSizeBytes + 1)
	_, err := svc.CreateComment("t1", "sp1", "u1", "", []CommentAttachmentInput{
		{FileURL: "https://obj/a.png", FileSize: &over},
	}, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("oversize attachment should be invalid, got %v", err)
	}
}

func TestCommentService_Create_WithTextAndAttachments_OK(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newCommentSvc(newFakeCommentRepo(), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
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
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	content := "hi"
	comment := &model.MatterComment{ID: "c1", MatterID: "t1", UserID: "u1", Content: &content}
	svc := newCommentSvc(newFakeCommentRepo(comment), newFakeCommentAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	err := svc.DeleteComment("c1", "space-A", "u2", "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-author DeleteComment: got %v, want ErrForbidden", err)
	}
}

func TestCommentService_Delete_AuthorRemovesAttachments(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	content := "hi"
	comment := &model.MatterComment{ID: "c1", MatterID: "t1", UserID: "u1", Content: &content}
	cmtRepo := newFakeCommentRepo(comment)
	attRepo := newFakeCommentAttachmentRepo()
	attRepo.byCommentID["c1"] = []*model.CommentAttachment{
		{ID: "a1", CommentID: "c1", FileURL: "https://obj/a.png"},
	}
	svc := newCommentSvc(cmtRepo, attRepo, newFakeMatterRepo(matter), fakeAccessChecker{})
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
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.SetStatus("t1", "sp1", "u1", "banana")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("invalid status should return ErrInvalidInput, got %v", err)
	}
}

func strPtr(s string) *string { return &s }
