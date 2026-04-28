package notification

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

func TestWKNotifier_Notify_DeduplicatesAndSkipsActor(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		// Extract channel_id from request to track who was notified.
		// For simplicity, just count calls.
		sent = append(sent, "called")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWKNotifier(srv.URL, "notification")

	// actorID="u1" should be skipped; "u2" appears twice but sent once; "" skipped.
	n.notify("u1", []string{"u1", "u2", "u3", "u2", ""}, "test msg")

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends, got %d", len(sent))
	}
}

func TestWKNotifier_Notify_EmptyTargets(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWKNotifier(srv.URL, "notification")
	n.notify("actor", nil, "msg")
	if called {
		t.Fatal("expected no sends for nil targets")
	}

	n.notify("actor", []string{}, "msg")
	if called {
		t.Fatal("expected no sends for empty targets")
	}
}

func TestWKNotifier_SendToUser_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewWKNotifier(srv.URL, "notification")
	err := n.sendToUser("u1", "test")
	if err == nil {
		t.Fatal("expected error for 500 response")
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

func TestNoop_ImplementsNotifier(t *testing.T) {
	var n Notifier = Noop{}
	todo := &model.Todo{Title: "x", CreatorID: "u1", Status: "open"}
	n.NotifyTodoCreated(todo, "name", []string{"u2"})
	n.NotifyStatusChanged(todo, "u1", "name", []string{"u2"})
	n.NotifyAssigneeAdded(todo, "name", "u3")
	n.NotifyCommentAdded(todo, "u1", "name", []string{"u2"})
}
