package notification

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

func TestSafeGo_RecoversPanic(t *testing.T) {
	done := make(chan bool, 1)
	SafeGo(func() {
		defer func() { done <- true }()
		panic("test panic")
	})
	<-done
}

func TestTemplates(t *testing.T) {
	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{"todoCreated", func() string { return todoCreatedMsg("Review PR", "Alice") }, "📋 新任务「Review PR」— Alice 分配给了你"},
		{"statusClosed", func() string { return statusChangedMsg("Review PR", "Bob", "closed") }, "📋 任务「Review PR」— Bob 关闭了"},
		{"statusReopened", func() string { return statusChangedMsg("Review PR", "Bob", "open") }, "📋 任务「Review PR」— Bob 重新打开了"},
		{"assigneeAdded", func() string { return assigneeAddedMsg("Review PR", "Alice") }, "📋 任务「Review PR」— Alice 将你添加为负责人"},
		{"commentAdded", func() string { return commentAddedMsg("Review PR", "Charlie") }, "📋 任务「Review PR」— Charlie 添加了评论"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(); got != tt.want {
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

func TestDedupTargets(t *testing.T) {
	tests := []struct {
		name    string
		actor   string
		uids    []string
		want    []string
	}{
		{"empty", "actor", []string{}, []string{}},
		{"removes empties", "actor", []string{"", "u1", ""}, []string{"u1"}},
		{"removes actor", "actor", []string{"actor", "u1"}, []string{"u1"}},
		{"removes duplicates", "actor", []string{"u1", "u2", "u1"}, []string{"u1", "u2"}},
		{"all together", "actor", []string{"", "actor", "u1", "u1", "u2", ""}, []string{"u1", "u2"}},
		{"empty actor keeps all", "", []string{"u1", "u2"}, []string{"u1", "u2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupTargets(tt.actor, tt.uids)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDmworkNotifier_NotifyTodoCreated(t *testing.T) {
	type captured struct {
		method string
		path   string
		token  string
		body   notifyRequest
	}
	got := make(chan captured, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req notifyRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("decode body: %v", err)
		}
		got <- captured{
			method: r.Method,
			path:   r.URL.Path,
			token:  r.Header.Get("X-Internal-Token"),
			body:   req,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewDmworkNotifier(srv.URL, "secret-token")
	todo := &model.Todo{
		ID:        "todo1",
		SpaceID:   "space1",
		Title:     "Test",
		CreatorID: "actor1",
		Status:    model.TodoStatusOpen,
	}
	n.NotifyTodoCreated(todo, "Alice", []string{"u2", "u3"})

	c := <-got
	if c.method != http.MethodPost {
		t.Errorf("method = %q, want POST", c.method)
	}
	if c.path != "/v1/internal/notify" {
		t.Errorf("path = %q, want /v1/internal/notify", c.path)
	}
	if c.token != "secret-token" {
		t.Errorf("X-Internal-Token = %q, want secret-token", c.token)
	}
	if c.body.SpaceID != "space1" {
		t.Errorf("space_id = %q, want space1", c.body.SpaceID)
	}
	if c.body.Service != notifyService {
		t.Errorf("service = %q, want %q", c.body.Service, notifyService)
	}
	if c.body.Event != eventTodoCreated {
		t.Errorf("event = %q, want %q", c.body.Event, eventTodoCreated)
	}
	if c.body.ActorUID != "actor1" {
		t.Errorf("actor_uid = %q, want actor1", c.body.ActorUID)
	}
	sort.Strings(c.body.Targets)
	wantTargets := []string{"u2", "u3"}
	if !reflect.DeepEqual(c.body.Targets, wantTargets) {
		t.Errorf("targets = %v, want %v", c.body.Targets, wantTargets)
	}
	if c.body.Payload["todo_id"] != "todo1" {
		t.Errorf("payload.todo_id = %v, want todo1", c.body.Payload["todo_id"])
	}
	if c.body.Payload["todo_title"] != "Test" {
		t.Errorf("payload.todo_title = %v, want Test", c.body.Payload["todo_title"])
	}
	if _, ok := c.body.Payload["message"].(string); !ok {
		t.Errorf("payload.message missing or not string: %v", c.body.Payload["message"])
	}
}
