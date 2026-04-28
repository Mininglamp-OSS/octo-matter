package notification

import (
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
