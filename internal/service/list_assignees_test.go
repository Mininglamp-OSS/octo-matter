package service

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// listSvcWithRepos builds a MatterService where the matter and assignee fakes
// can be inspected/seeded. IM checker is a no-op (allow=true) so channel
// gating doesn't interfere.
func listSvcWithRepos(matters *fakeMatterRepo, assignees *fakeAssigneeRepo) *MatterService {
	return NewMatterService(matters, assignees, fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, nil)
}

func mustList(t *testing.T, svc *MatterService, spaceID string, filter repository.MatterFilter, token string) []*MatterListItem {
	t.Helper()
	res, err := svc.ListMatters(context.Background(), spaceID, filter, token)
	if err != nil {
		t.Fatalf("ListMatters returned error: %v", err)
	}
	if res == nil {
		t.Fatalf("ListMatters returned nil result")
	}
	return res.Items
}

// findItem locates a list item by matter ID; fails the test if absent.
func findItem(t *testing.T, items []*MatterListItem, id string) *MatterListItem {
	t.Helper()
	for _, it := range items {
		if it == nil || it.Matter == nil {
			continue
		}
		if it.Matter.ID == id {
			return it
		}
	}
	t.Fatalf("no list item with matter id %q (items=%d)", id, len(items))
	return nil
}

func userIDs(as []*model.MatterAssignee) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.UserID)
	}
	sort.Strings(out)
	return out
}

// TestListMatters_AttachesAssigneesPerMatter — each matter in the list
// response carries its own assignees, hydrated from the repo. No cross-matter
// leakage. Empty-assignees matter still gets a non-nil empty slice (not null
// in JSON).
func TestListMatters_AttachesAssigneesPerMatter(t *testing.T) {
	now := time.Now()
	mA := &model.Matter{ID: "m-a", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen, CreatedAt: now}
	mB := &model.Matter{ID: "m-b", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen, CreatedAt: now}
	mC := &model.Matter{ID: "m-c", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen, CreatedAt: now}
	matters := newFakeMatterRepo(mA, mB, mC)

	assignees := newFakeAssigneeRepo()
	_ = assignees.Create(context.Background(), &model.MatterAssignee{ID: "a1", MatterID: "m-a", UserID: "u1"})
	_ = assignees.Create(context.Background(), &model.MatterAssignee{ID: "a2", MatterID: "m-a", UserID: "u2"})
	_ = assignees.Create(context.Background(), &model.MatterAssignee{ID: "a3", MatterID: "m-b", UserID: "u3"})
	// m-c intentionally has no assignees.

	svc := listSvcWithRepos(matters, assignees)
	items := mustList(t, svc, "sp1", repository.MatterFilter{CallerUIDs: []string{"owner"}}, "")

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	itemA := findItem(t, items, "m-a")
	if got := userIDs(itemA.Assignees); !equalStrings(got, []string{"u1", "u2"}) {
		t.Errorf("matter m-a assignees = %v, want [u1 u2]", got)
	}

	itemB := findItem(t, items, "m-b")
	if got := userIDs(itemB.Assignees); !equalStrings(got, []string{"u3"}) {
		t.Errorf("matter m-b assignees = %v, want [u3]", got)
	}

	itemC := findItem(t, items, "m-c")
	if itemC.Assignees == nil {
		t.Errorf("matter with no assignees must return empty slice, not nil")
	}
	if len(itemC.Assignees) != 0 {
		t.Errorf("matter m-c assignees = %v, want empty", itemC.Assignees)
	}
}

// TestListMatters_BatchFetchesAssignees_NoNPlusOne — the service must fetch
// all assignees in a single batch call (ListByMatterIDs), not one query per
// matter. Counted via a tracking wrapper.
func TestListMatters_BatchFetchesAssignees_NoNPlusOne(t *testing.T) {
	now := time.Now()
	ms := []*model.Matter{
		{ID: "m1", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen, CreatedAt: now},
		{ID: "m2", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen, CreatedAt: now},
		{ID: "m3", SpaceID: "sp1", CreatorID: "owner", Status: model.MatterStatusOpen, CreatedAt: now},
	}
	matters := newFakeMatterRepo(ms...)

	tracker := &trackingAssigneeRepo{fakeAssigneeRepo: newFakeAssigneeRepo()}
	_ = tracker.Create(context.Background(), &model.MatterAssignee{ID: "a1", MatterID: "m1", UserID: "u1"})
	_ = tracker.Create(context.Background(), &model.MatterAssignee{ID: "a2", MatterID: "m2", UserID: "u2"})

	svc := NewMatterService(matters, tracker, fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, nil)

	_, err := svc.ListMatters(context.Background(), "sp1", repository.MatterFilter{CallerUIDs: []string{"owner"}}, "")
	if err != nil {
		t.Fatalf("ListMatters error: %v", err)
	}

	if tracker.listByMatterCalls != 0 {
		t.Errorf("ListMatters must NOT call per-matter ListByMatter; got %d calls", tracker.listByMatterCalls)
	}
	if tracker.listByMatterIDsCalls != 1 {
		t.Errorf("ListMatters should call ListByMatterIDs exactly once for batch fetch; got %d", tracker.listByMatterIDsCalls)
	}
	wantIDs := []string{"m1", "m2", "m3"}
	gotIDs := append([]string(nil), tracker.lastBatchIDs...)
	sort.Strings(gotIDs)
	if !equalStrings(gotIDs, wantIDs) {
		t.Errorf("ListByMatterIDs called with %v, want %v", gotIDs, wantIDs)
	}
}

// TestListMatters_EmptyResult_DoesNotQueryAssignees — when the matter query
// returns nothing, skip the assignee batch call entirely.
func TestListMatters_EmptyResult_DoesNotQueryAssignees(t *testing.T) {
	matters := newFakeMatterRepo() // empty
	tracker := &trackingAssigneeRepo{fakeAssigneeRepo: newFakeAssigneeRepo()}

	svc := NewMatterService(matters, tracker, fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, nil)
	res, err := svc.ListMatters(context.Background(), "sp1", repository.MatterFilter{CallerUIDs: []string{"owner"}}, "")
	if err != nil {
		t.Fatalf("ListMatters error: %v", err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(res.Items))
	}
	if tracker.listByMatterIDsCalls != 0 {
		t.Errorf("empty matter list should skip assignee fetch; got %d calls", tracker.listByMatterIDsCalls)
	}
}

// TestFakeAssigneeRepo_ListByMatterIDs_EmptyInput — contract: empty input
// returns an empty slice without error and without performing any work.
func TestFakeAssigneeRepo_ListByMatterIDs_EmptyInput(t *testing.T) {
	repo := newFakeAssigneeRepo()
	got, err := repo.ListByMatterIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d", len(got))
	}
}

// trackingAssigneeRepo wraps fakeAssigneeRepo to count which path the service
// took (per-matter vs batch).
type trackingAssigneeRepo struct {
	*fakeAssigneeRepo
	listByMatterCalls    int
	listByMatterIDsCalls int
	lastBatchIDs         []string
}

func (t *trackingAssigneeRepo) ListByMatter(ctx context.Context, matterID string) ([]*model.MatterAssignee, error) {
	t.listByMatterCalls++
	return t.fakeAssigneeRepo.ListByMatter(ctx, matterID)
}

func (t *trackingAssigneeRepo) ListByMatterIDs(ctx context.Context, matterIDs []string) ([]*model.MatterAssignee, error) {
	t.listByMatterIDsCalls++
	t.lastBatchIDs = append([]string(nil), matterIDs...)
	return t.fakeAssigneeRepo.ListByMatterIDs(ctx, matterIDs)
}

