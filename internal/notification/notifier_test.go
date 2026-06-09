package notification

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
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

// TestNotificationWording pins the default-language (zh-CN) notification copy
// rendered from the catalog keys + params — the same strings the payload's
// fallback `message` carries.
func TestNotificationWording(t *testing.T) {
	const zh = i18n.LangZhCN
	statusDone := i18n.Localize(zh, i18n.KeyNotifyStatusChanged, map[string]any{
		"Title": "Review PR", "Actor": "Bob",
		"Action": i18n.Localize(zh, actionKey("done"), nil),
	})
	statusOpen := i18n.Localize(zh, i18n.KeyNotifyStatusChanged, map[string]any{
		"Title": "Review PR", "Actor": "Bob",
		"Action": i18n.Localize(zh, actionKey("open"), nil),
	})
	tests := []struct {
		name, got, want string
	}{
		{"matterCreated", i18n.Localize(zh, i18n.KeyNotifyMatterCreated, map[string]any{"Title": "Review PR", "Actor": "Alice"}), "📋 新任务「Review PR」— Alice 分配给了你"},
		{"statusDone", statusDone, "📋 任务「Review PR」— Bob 完成了"},
		{"statusReopened", statusOpen, "📋 任务「Review PR」— Bob 重新打开了"},
		{"assigneeAdded", i18n.Localize(zh, i18n.KeyNotifyAssigneeAdded, map[string]any{"Title": "Review PR", "Actor": "Alice"}), "📋 任务「Review PR」— Alice 将你添加为负责人"},
		{"timelineEntryAdded", i18n.Localize(zh, i18n.KeyNotifyTimelineEntryAdded, map[string]any{"Title": "Review PR", "Actor": "Charlie"}), "📋 任务「Review PR」— Charlie 添加了进展"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestNoop_ImplementsNotifier(t *testing.T) {
	var n Notifier = Noop{}
	matter := &model.Matter{Title: "x", CreatorID: "u1", Status: "open"}
	n.NotifyMatterCreated(matter, "name", []string{"u2"})
	n.NotifyStatusChanged(matter, "u1", "name", []string{"u2"}, []string{"u4"})
	n.NotifyAssigneeAdded(matter, "name", "u3")
	n.NotifyTimelineEntryAdded(matter, "u1", "name", []string{"u2"}, []string{"u4"})
}

func TestDedupTargets(t *testing.T) {
	tests := []struct {
		name  string
		actor string
		uids  []string
		want  []string
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

func TestOctoNotifier_NotifyMatterCreated(t *testing.T) {
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

	n := NewOctoNotifier(srv.URL, "secret-token", "zh-CN")
	matter := &model.Matter{
		ID:        "matter1",
		SpaceID:   "space1",
		Title:     "Test",
		CreatorID: "actor1",
		Status:    model.MatterStatusOpen,
	}
	n.NotifyMatterCreated(matter, "Alice", []string{"u2", "u3"})

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
	if c.body.Event != eventMatterCreated {
		t.Errorf("event = %q, want %q", c.body.Event, eventMatterCreated)
	}
	if c.body.ActorUID != "actor1" {
		t.Errorf("actor_uid = %q, want actor1", c.body.ActorUID)
	}
	sort.Strings(c.body.Targets)
	wantTargets := []string{"u2", "u3"}
	if !reflect.DeepEqual(c.body.Targets, wantTargets) {
		t.Errorf("targets = %v, want %v", c.body.Targets, wantTargets)
	}
	if c.body.Payload["matter_id"] != "matter1" {
		t.Errorf("payload.todo_id = %v, want matter1", c.body.Payload["matter_id"])
	}
	if c.body.Payload["matter_title"] != "Test" {
		t.Errorf("payload.todo_title = %v, want Test", c.body.Payload["matter_title"])
	}
	if _, ok := c.body.Payload["message"].(string); !ok {
		t.Errorf("payload.message missing or not string: %v", c.body.Payload["message"])
	}
	// Structured fields the IM server uses to localize per recipient.
	if c.body.Payload["message_key"] != "notify.matter_created" {
		t.Errorf("payload.message_key = %v, want notify.matter_created", c.body.Payload["message_key"])
	}
	params, ok := c.body.Payload["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("payload.params missing or wrong type: %v", c.body.Payload["params"])
	}
	if params["Title"] != "Test" || params["Actor"] != "Alice" {
		t.Errorf("payload.params = %v, want Title=Test Actor=Alice", params)
	}
}
