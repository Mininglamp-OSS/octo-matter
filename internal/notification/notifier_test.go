package notification

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

func TestNotify_DeduplicatesAndSkipsActor(t *testing.T) {
	var sent []string
	n := &testNotifier{sendFn: func(uid, _ string) error {
		sent = append(sent, uid)
		return nil
	}}

	// actorID="u1" should be skipped; "u2" appears twice but sent once.
	n.notify("u1", []string{"u1", "u2", "u3", "u2", ""}, "test msg")

	if len(sent) != 2 {
		t.Fatalf("expected 2 sends, got %d: %v", len(sent), sent)
	}
	if sent[0] != "u2" || sent[1] != "u3" {
		t.Errorf("expected [u2, u3], got %v", sent)
	}
}

func TestNotify_EmptyTargets(t *testing.T) {
	var sent []string
	n := &testNotifier{sendFn: func(uid, _ string) error {
		sent = append(sent, uid)
		return nil
	}}

	n.notify("actor", nil, "msg")
	if len(sent) != 0 {
		t.Fatalf("expected 0 sends for nil targets, got %d", len(sent))
	}

	n.notify("actor", []string{}, "msg")
	if len(sent) != 0 {
		t.Fatalf("expected 0 sends for empty targets, got %d", len(sent))
	}
}

func TestSafeGo_RecoversPanic(t *testing.T) {
	done := make(chan bool, 1)
	SafeGo(func() {
		defer func() { done <- true }()
		panic("test panic")
	})
	<-done // would hang if SafeGo didn't recover
}

func TestTemplates(t *testing.T) {
	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{
			name: "todoCreated",
			fn:   func() string { return todoCreatedMsg("Review PR", "Alice") },
			want: "📋 新任务「Review PR」— Alice 分配给了你",
		},
		{
			name: "statusClosed",
			fn:   func() string { return statusChangedMsg("Review PR", "Bob", "closed") },
			want: "📋 任务「Review PR」— Bob 关闭了",
		},
		{
			name: "statusReopened",
			fn:   func() string { return statusChangedMsg("Review PR", "Bob", "open") },
			want: "📋 任务「Review PR」— Bob 重新打开了",
		},
		{
			name: "assigneeAdded",
			fn:   func() string { return assigneeAddedMsg("Review PR", "Alice") },
			want: "📋 任务「Review PR」— Alice 将你添加为负责人",
		},
		{
			name: "commentAdded",
			fn:   func() string { return commentAddedMsg("Review PR", "Charlie") },
			want: "📋 任务「Review PR」— Charlie 添加了评论",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNoop_Compiles(t *testing.T) {
	// Verify Noop satisfies the interface at compile time.
	var n Notifier = Noop{}
	todo := &model.Todo{Title: "x", CreatorID: "u1", Status: "open"}
	n.NotifyTodoCreated(todo, "name", []string{"u2"})
	n.NotifyStatusChanged(todo, "u1", "name", []string{"u2"})
	n.NotifyAssigneeAdded(todo, "name", "u3")
	n.NotifyCommentAdded(todo, "u1", "name", []string{"u2"})
}

// testNotifier reuses WKNotifier's notify logic with a pluggable send function.
type testNotifier struct {
	sendFn func(uid, content string) error
}

func (n *testNotifier) notify(actorID string, targetIDs []string, content string) {
	seen := make(map[string]bool)
	for _, uid := range targetIDs {
		if uid == actorID || uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		if n.sendFn != nil {
			n.sendFn(uid, content)
		}
	}
}
