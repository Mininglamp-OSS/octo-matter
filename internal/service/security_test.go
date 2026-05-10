package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

func (f *fakeMatterRepo) Create(_ context.Context, matter *model.Matter) error {
	if matter.ID == "" {
		matter.ID = "generated-" + matter.Title
	}
	f.matters[matter.ID] = matter
	return nil
}

func (f *fakeMatterRepo) GetByID(_ context.Context, id, spaceID string) (*model.Matter, error) {
	t, ok := f.matters[id]
	if !ok || t.SpaceID != spaceID || t.DeletedAt != nil {
		return nil, apperr.ErrNotFound
	}
	return t, nil
}

func (f *fakeMatterRepo) ListBySpace(_ context.Context, spaceID string, _ repository.MatterFilter) ([]*model.Matter, bool, error) {
	var out []*model.Matter
	for _, t := range f.matters {
		if t.SpaceID == spaceID && t.DeletedAt == nil {
			out = append(out, t)
		}
	}
	return out, false, nil
}

func (f *fakeMatterRepo) Update(_ context.Context, matter *model.Matter) error {
	existing, ok := f.matters[matter.ID]
	if !ok || existing.SpaceID != matter.SpaceID {
		return apperr.ErrNotFound
	}
	f.matters[matter.ID] = matter
	return nil
}

func (f *fakeMatterRepo) UpdateStatus(ctx context.Context, id, spaceID, status string) error {
	t, err := f.GetByID(ctx, id, spaceID)
	if err != nil {
		return err
	}
	t.Status = model.MatterStatus(status)
	return nil
}

func (f *fakeMatterRepo) SoftDelete(ctx context.Context, id, spaceID string) error {
	t, err := f.GetByID(ctx, id, spaceID)
	if err != nil {
		return err
	}
	now := time.Now()
	t.DeletedAt = &now
	return nil
}

func (f *fakeMatterRepo) HasAccess(_ context.Context, matterID string, callerUIDs []string, channelID string) (bool, error) {
	// For unit tests: always return false (creator check is in-memory anyway).
	// Tests needing participant/assignee/channel access use tracking fakes via full constructor.
	return false, nil
}

// --- assignee fake -------------------------------------------------------

type fakeAssigneeRepo struct {
	byMatter map[string][]*model.MatterAssignee
}

func newFakeAssigneeRepo() *fakeAssigneeRepo {
	return &fakeAssigneeRepo{byMatter: make(map[string][]*model.MatterAssignee)}
}

func (f *fakeAssigneeRepo) Create(_ context.Context, a *model.MatterAssignee) error {
	f.byMatter[a.MatterID] = append(f.byMatter[a.MatterID], a)
	return nil
}

func (f *fakeAssigneeRepo) Delete(_ context.Context, matterID, userID string) error {
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

func (f *fakeAssigneeRepo) ListByMatter(_ context.Context, matterID string) ([]*model.MatterAssignee, error) {
	return f.byMatter[matterID], nil
}

func (f *fakeAssigneeRepo) IsAssignee(_ context.Context, matterID, userID string) (bool, error) {
	for _, a := range f.byMatter[matterID] {
		if a.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeAssigneeRepo) IsAssigneeAny(_ context.Context, matterID string, userIDs []string) (bool, error) {
	for _, a := range f.byMatter[matterID] {
		for _, uid := range userIDs {
			if a.UserID == uid {
				return true, nil
			}
		}
	}
	return false, nil
}

// --- tx runner fakes -----------------------------------------------------

// noopTxRunner fails any Do() call.
type noopTxRunner struct{}

func (noopTxRunner) Do(_ context.Context, fn func(r *repository.TxRepos) error) error {
	return fmt.Errorf("transaction exercised (test stub — requires integration test)")
}

// fakeTimelineTx runs the closure directly against the supplied fake repos.
type fakeTimelineTx struct {
	timelines   TimelineStore
	attachments TimelineAttachmentStore
	channels    TimelineMatterChannelStore
}

func (f fakeTimelineTx) Do(_ context.Context, fn func(TimelineStore, TimelineAttachmentStore, ParticipantUpserter, TimelineMatterChannelStore) error) error {
	cs := f.channels
	if cs == nil {
		cs = fakeChannelRepo{}
	}
	return fn(f.timelines, f.attachments, fakeParticipantRepo{}, cs)
}

// --- access fakes --------------------------------------------------------

type fakeAccessChecker struct{}

func (fakeAccessChecker) CanAccessMatter(_ context.Context, _ *model.Matter, _ []string, _, _ string) (bool, error) {
	return true, nil
}

// --- participant fake ---

type fakeParticipantRepo struct{}

func (fakeParticipantRepo) Upsert(_ context.Context, matterID, userID string) error { return nil }
func (fakeParticipantRepo) IsParticipantAny(_ context.Context, matterID string, userIDs []string) (bool, error) {
	return false, nil
}
func (fakeParticipantRepo) ListUserIDs(_ context.Context, matterID string) ([]string, error) {
	return nil, nil
}

// --- channel fake ---

type fakeChannelRepo struct{}

func (fakeChannelRepo) FindByMatterAndChannelID(_ context.Context, matterID, channelID string) (*model.MatterChannel, error) {
	return nil, apperr.ErrNotFound
}
func (fakeChannelRepo) Create(_ context.Context, mc *model.MatterChannel) error    { return nil }
func (fakeChannelRepo) Delete(_ context.Context, matterID, channelID string) error { return nil }
func (fakeChannelRepo) IsLinkedChannel(_ context.Context, matterID, channelID string) (bool, error) {
	return false, nil
}
func (fakeChannelRepo) ListByMatter(_ context.Context, matterID string) ([]*model.MatterChannel, error) {
	return nil, nil
}

// --- helpers -------------------------------------------------------------

func newMatterSvc(matterRepo matterStore, assigneeRepo assigneeStore) *MatterService {
	return NewMatterService(matterRepo, assigneeRepo, fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, nil)
}

func newTimelineSvc(
	timelineRepo *fakeTimelineRepo,
	attachmentRepo *fakeTimelineAttachmentRepo,
	matterRepo *fakeMatterRepo,
	access MatterAccessChecker,
) *TimelineService {
	return NewTimelineService(
		nil,
		timelineRepo,
		attachmentRepo,
		matterRepo,
		access,
		nil, // linkVerifier — these tests don't exercise auto-link
		fakeTimelineTx{timelines: timelineRepo, attachments: attachmentRepo},
		fakeParticipantRepo{},
		newFakeAssigneeRepo(),
		nil,
	)
}

func createTimelineEntry(
	ctx context.Context,
	svc *TimelineService,
	matterID, spaceID string,
	callerUIDs []string,
	actorUID, content string,
	attachments []TimelineAttachmentInput,
	sourceChannelID string,
) (*model.TimelineEntry, error) {
	entry, _, err := svc.CreateEntry(ctx, TimelineInput{
		MatterID:    matterID,
		SpaceID:     spaceID,
		ActorUID:    actorUID,
		CallerUIDs:  callerUIDs,
		Content:     content,
		Attachments: attachments,
		ChannelID:   sourceChannelID,
	})
	return entry, err
}

// --- timeline/attachment fakes -------------------------------------------

type fakeTimelineRepo struct {
	byID map[string]*model.TimelineEntry
}

func newFakeTimelineRepo(cs ...*model.TimelineEntry) *fakeTimelineRepo {
	m := make(map[string]*model.TimelineEntry, len(cs))
	for _, c := range cs {
		m[c.ID] = c
	}
	return &fakeTimelineRepo{byID: m}
}

func (f *fakeTimelineRepo) Create(_ context.Context, c *model.TimelineEntry) error {
	if c.ID == "" {
		c.ID = fmt.Sprintf("c-%d", len(f.byID)+1)
	}
	f.byID[c.ID] = c
	return nil
}

func (f *fakeTimelineRepo) GetByID(_ context.Context, id string) (*model.TimelineEntry, error) {
	c, ok := f.byID[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	return c, nil
}

func (f *fakeTimelineRepo) Delete(_ context.Context, id string) error {
	delete(f.byID, id)
	return nil
}

func (f *fakeTimelineRepo) ListByMatter(_ context.Context, matterID, sourceChannelID string, cursor *string, limit int) ([]*model.TimelineEntry, bool, error) {
	var out []*model.TimelineEntry
	for _, c := range f.byID {
		if c.MatterID != matterID {
			continue
		}
		if sourceChannelID != "" {
			if c.SourceChannelID == nil || *c.SourceChannelID != sourceChannelID {
				continue
			}
		}
		out = append(out, c)
	}
	return out, false, nil
}

func (f *fakeTimelineRepo) ListRecentByMatter(_ context.Context, matterID string, limit int) ([]*model.TimelineEntry, error) {
	var out []*model.TimelineEntry
	for _, c := range f.byID {
		if c.MatterID == matterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type fakeTimelineAttachmentRepo struct {
	byEntryID map[string][]*model.TimelineAttachment
}

func newFakeTimelineAttachmentRepo() *fakeTimelineAttachmentRepo {
	return &fakeTimelineAttachmentRepo{byEntryID: make(map[string][]*model.TimelineAttachment)}
}

func (f *fakeTimelineAttachmentRepo) CreateMany(_ context.Context, atts []*model.TimelineAttachment) error {
	for i, a := range atts {
		if a.ID == "" {
			a.ID = fmt.Sprintf("a-%d-%d", len(f.byEntryID), i)
		}
		f.byEntryID[a.EntryID] = append(f.byEntryID[a.EntryID], a)
	}
	return nil
}

func (f *fakeTimelineAttachmentRepo) ListByEntryIDs(_ context.Context, ids []string) (map[string][]model.TimelineAttachment, error) {
	out := make(map[string][]model.TimelineAttachment, len(ids))
	for _, id := range ids {
		for _, a := range f.byEntryID[id] {
			out[id] = append(out[id], *a)
		}
	}
	return out, nil
}

func (f *fakeTimelineAttachmentRepo) DeleteByEntryID(_ context.Context, timelineID string) error {
	delete(f.byEntryID, timelineID)
	return nil
}

// --- Tests ---------------------------------------------------------------

func TestMatterService_GetMatter_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.GetMatter(context.Background(), "t1", "space-B", []string{"caller"}, "", "")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space GetMatter: got %v, want ErrNotFound", err)
	}
}

func TestMatterService_UpdateMatter_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.UpdateMatter(context.Background(), "t1", "space-B", []string{"u1"}, strPtr("new title"), nil, nil, nil)
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space UpdateMatter: got %v, want ErrNotFound", err)
	}
}

func TestMatterService_UpdateMatter_NonCreatorReturnsForbidden(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.UpdateMatter(context.Background(), "t1", "space-A", []string{"u2"}, strPtr("new"), nil, nil, nil)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-creator UpdateMatter: got %v, want ErrForbidden", err)
	}
}

func TestMatterService_SoftDelete_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	err := svc.SoftDelete(context.Background(), "t1", "space-B", []string{"u1"})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space SoftDelete: got %v, want ErrNotFound", err)
	}
}

func TestTimelineService_Create_CrossSpaceReturnsNotFound(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	_, err := createTimelineEntry(context.Background(), svc, "t1", "space-B", []string{"u2"}, "u2", "hi", nil, "")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("cross-space CreateEntry: got %v, want ErrNotFound", err)
	}
}

func TestTimelineService_Create_EmptyRejected(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	_, err := createTimelineEntry(context.Background(), svc, "t1", "sp1", []string{"u1"}, "u1", "", nil, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("empty timeline should be invalid, got %v", err)
	}
}

func TestTimelineService_Create_AttachmentsOnly_OK(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	c, err := createTimelineEntry(context.Background(), svc, "t1", "sp1", []string{"u1"}, "u1", "", []TimelineAttachmentInput{
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

func TestTimelineService_Create_TooManyAttachments(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	atts := make([]TimelineAttachmentInput, MaxAttachmentsPerEntry+1)
	for i := range atts {
		atts[i] = TimelineAttachmentInput{FileURL: "https://obj/a.png"}
	}
	_, err := createTimelineEntry(context.Background(), svc, "t1", "sp1", []string{"u1"}, "u1", "hi", atts, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("too-many attachments should be invalid, got %v", err)
	}
}

func TestTimelineService_Create_AttachmentTooLarge(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	over := int64(MaxAttachmentSizeBytes + 1)
	_, err := createTimelineEntry(context.Background(), svc, "t1", "sp1", []string{"u1"}, "u1", "", []TimelineAttachmentInput{
		{FileURL: "https://obj/a.png", FileSize: &over},
	}, "")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("oversize attachment should be invalid, got %v", err)
	}
}

func TestTimelineService_Create_WithTextAndAttachments_OK(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	svc := newTimelineSvc(newFakeTimelineRepo(), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	c, err := createTimelineEntry(context.Background(), svc, "t1", "sp1", []string{"u1"}, "u1", "look at this", []TimelineAttachmentInput{
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

func TestTimelineService_Delete_NonAuthorReturnsForbidden(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "space-A", CreatorID: "u1", Title: "x"}
	content := "hi"
	timeline := &model.TimelineEntry{ID: "c1", MatterID: "t1", UserID: "u1", Content: &content}
	svc := newTimelineSvc(newFakeTimelineRepo(timeline), newFakeTimelineAttachmentRepo(), newFakeMatterRepo(matter), fakeAccessChecker{})
	err := svc.DeleteEntry(context.Background(), "t1", "c1", "space-A", []string{"u2"}, "u2", "", "")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-author DeleteEntry: got %v, want ErrForbidden", err)
	}
}

func TestTimelineService_Delete_AuthorRemovesAttachments(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Title: "x"}
	content := "hi"
	timeline := &model.TimelineEntry{ID: "c1", MatterID: "t1", UserID: "u1", Content: &content}
	cmtRepo := newFakeTimelineRepo(timeline)
	attRepo := newFakeTimelineAttachmentRepo()
	attRepo.byEntryID["c1"] = []*model.TimelineAttachment{
		{ID: "a1", EntryID: "c1", FileURL: "https://obj/a.png"},
	}
	svc := newTimelineSvc(cmtRepo, attRepo, newFakeMatterRepo(matter), fakeAccessChecker{})
	if err := svc.DeleteEntry(context.Background(), "t1", "c1", "sp1", []string{"u1"}, "u1", "", ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := cmtRepo.byID["c1"]; ok {
		t.Fatalf("timeline not removed")
	}
	if _, ok := attRepo.byEntryID["c1"]; ok {
		t.Fatalf("attachments not removed")
	}
}

// TestTimelineService_Delete_MatterIDMismatch_ReturnsNotFound verifies that
// DELETE /matters/A/timeline/<entry-from-B> does not let the caller delete an
// entry that belongs to a different matter, even if they author the entry and
// have access to that other matter.
func TestTimelineService_Delete_MatterIDMismatch_ReturnsNotFound(t *testing.T) {
	matterA := &model.Matter{ID: "ma", SpaceID: "sp1", CreatorID: "u1", Title: "A"}
	matterB := &model.Matter{ID: "mb", SpaceID: "sp1", CreatorID: "u1", Title: "B"}
	content := "entry on B"
	entry := &model.TimelineEntry{ID: "e-on-b", MatterID: "mb", UserID: "u1", Content: &content}
	svc := newTimelineSvc(
		newFakeTimelineRepo(entry),
		newFakeTimelineAttachmentRepo(),
		newFakeMatterRepo(matterA, matterB),
		fakeAccessChecker{},
	)
	// Caller hits /matters/ma/timeline/e-on-b — matterID in path is A, entry is on B.
	err := svc.DeleteEntry(context.Background(), "ma", "e-on-b", "sp1", []string{"u1"}, "u1", "", "")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("path matter id mismatch: got %v, want ErrNotFound", err)
	}
}

func TestSetStatus_RejectsInvalidStatus(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "u1", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	_, err := svc.SetStatus(context.Background(), "t1", "sp1", []string{"u1"}, "banana")
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("invalid status should return ErrInvalidInput, got %v", err)
	}
}

func strPtr(s string) *string { return &s }

// --- New permission path tests (review feedback #1) ---

func TestMatterService_GetMatter_ParticipantCanAccess(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	// HasAccess returns true when userID is a participant
	matterRepo := &hasAccessFakeMatterRepo{fakeMatterRepo: *newFakeMatterRepo(matter), accessGrants: map[string]bool{"t1:viewer:": true}}
	svc := NewMatterService(matterRepo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, nil)
	_, err := svc.GetMatter(context.Background(), "t1", "sp1", []string{"viewer"}, "", "")
	if err != nil {
		t.Fatalf("participant should have access, got %v", err)
	}
}

// TestMatterService_GetMatter_ChannelMemberCanAccess covers the IM-verified
// channel-member path. Caller supplies channel-id + token; the IM stub returns
// true; canAccessMatter then keeps channelID and HasAccess grants via channel.
func TestMatterService_GetMatter_ChannelMemberCanAccess(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	matterRepo := &hasAccessFakeMatterRepo{fakeMatterRepo: *newFakeMatterRepo(matter), accessGrants: map[string]bool{"t1:stranger:ch-123": true}}
	im := &spyIMChecker{allow: true}
	svc := NewMatterService(matterRepo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, im)
	_, err := svc.GetMatter(context.Background(), "t1", "sp1", []string{"stranger"}, "ch-123", "user-tok")
	if err != nil {
		t.Fatalf("IM-verified channel member should have access, got %v", err)
	}
}

func TestMatterService_SetStatus_AssigneeCannotArchive(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	assigneeRepo := newFakeAssigneeRepo()
	_ = assigneeRepo.Create(context.Background(), &model.MatterAssignee{MatterID: "t1", UserID: "worker"})
	svc := newMatterSvc(newFakeMatterRepo(matter), assigneeRepo)
	_, err := svc.SetStatus(context.Background(), "t1", "sp1", []string{"worker"}, model.MatterStatusArchived)
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("assignee should not be able to archive, got %v", err)
	}
}

func TestMatterService_SetStatus_CreatorCanArchive(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	svc := newMatterSvc(newFakeMatterRepo(matter), newFakeAssigneeRepo())
	// Pre-check passes (creator), but noopTxRunner errors — verify it's not ErrForbidden
	_, err := svc.SetStatus(context.Background(), "t1", "sp1", []string{"owner"}, model.MatterStatusArchived)
	if errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("creator should be able to archive, got forbidden")
	}
}

func TestMatterService_RemoveAssignee_SelfUnassign(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	assigneeRepo := newFakeAssigneeRepo()
	_ = assigneeRepo.Create(context.Background(), &model.MatterAssignee{MatterID: "t1", UserID: "worker"})
	svc := newMatterSvc(newFakeMatterRepo(matter), assigneeRepo)
	err := svc.RemoveAssignee(context.Background(), "t1", "sp1", []string{"worker"}, "worker")
	if err != nil {
		t.Fatalf("self-unassign should succeed, got %v", err)
	}
}

func TestMatterService_RemoveAssignee_NonCreatorCannotRemoveOthers(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	assigneeRepo := newFakeAssigneeRepo()
	_ = assigneeRepo.Create(context.Background(), &model.MatterAssignee{MatterID: "t1", UserID: "worker1"})
	_ = assigneeRepo.Create(context.Background(), &model.MatterAssignee{MatterID: "t1", UserID: "worker2"})
	svc := newMatterSvc(newFakeMatterRepo(matter), assigneeRepo)
	err := svc.RemoveAssignee(context.Background(), "t1", "sp1", []string{"worker1"}, "worker2")
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("non-creator should not remove others, got %v", err)
	}
}

// TestMatterService_GetMatter_PopulatesParticipantsAndChannels verifies that
// GetMatter enriches the MatterDetail with participants and linked channels
// from the respective repositories.
func TestMatterService_GetMatter_PopulatesParticipantsAndChannels(t *testing.T) {
	matter := &model.Matter{ID: "t1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen}
	partRepo := &trackingParticipantRepo{
		participants: map[string][]string{"t1": {"p1", "p2"}},
	}
	chanRepo := &trackingChannelRepo{
		channels: map[string][]*model.MatterChannel{
			"t1": {{ID: "mc1", MatterID: "t1", ChannelID: "ch1", ChannelType: 1}},
		},
	}
	svc := NewMatterService(newFakeMatterRepo(matter), newFakeAssigneeRepo(), partRepo, chanRepo, nil, noopTxRunner{}, nil)
	detail, err := svc.GetMatter(context.Background(), "t1", "sp1", []string{"owner"}, "", "")
	if err != nil {
		t.Fatalf("GetMatter failed: %v", err)
	}
	if len(detail.Participants) != 2 || detail.Participants[0] != "p1" || detail.Participants[1] != "p2" {
		t.Fatalf("participants not populated correctly: %#v", detail.Participants)
	}
	if len(detail.Channels) != 1 || detail.Channels[0].ChannelID != "ch1" {
		t.Fatalf("channels not populated correctly: %#v", detail.Channels)
	}
}

// --- tracking fakes for new permission paths ---

// hasAccessFakeMatterRepo extends fakeMatterRepo with configurable HasAccess results.
type hasAccessFakeMatterRepo struct {
	fakeMatterRepo
	accessGrants map[string]bool // key: "matterID:uid:channelID"
}

func (f *hasAccessFakeMatterRepo) HasAccess(_ context.Context, matterID string, callerUIDs []string, channelID string) (bool, error) {
	for _, uid := range callerUIDs {
		key := matterID + ":" + uid + ":" + channelID
		if f.accessGrants[key] {
			return true, nil
		}
	}
	return false, nil
}

type trackingParticipantRepo struct {
	participants map[string][]string // matterID -> []userID
}

func (f *trackingParticipantRepo) Upsert(_ context.Context, matterID, userID string) error {
	return nil
}
func (f *trackingParticipantRepo) IsParticipantAny(_ context.Context, matterID string, userIDs []string) (bool, error) {
	for _, pid := range f.participants[matterID] {
		for _, uid := range userIDs {
			if pid == uid {
				return true, nil
			}
		}
	}
	return false, nil
}
func (f *trackingParticipantRepo) ListUserIDs(_ context.Context, matterID string) ([]string, error) {
	return f.participants[matterID], nil
}

type trackingChannelRepo struct {
	links    map[string][]string               // matterID -> []channelID
	channels map[string][]*model.MatterChannel // matterID -> []channel entries
}

func (f *trackingChannelRepo) Create(_ context.Context, mc *model.MatterChannel) error {
	return nil
}
func (f *trackingChannelRepo) Delete(_ context.Context, matterID, channelID string) error {
	return nil
}
func (f *trackingChannelRepo) IsLinkedChannel(_ context.Context, matterID, channelID string) (bool, error) {
	for _, cid := range f.links[matterID] {
		if cid == channelID {
			return true, nil
		}
	}
	return false, nil
}
func (f *trackingChannelRepo) ListByMatter(_ context.Context, matterID string) ([]*model.MatterChannel, error) {
	return f.channels[matterID], nil
}
