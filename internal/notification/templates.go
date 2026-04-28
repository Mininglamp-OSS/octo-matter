package notification

import "fmt"

// Message templates for system notifications. All return plain text —
// the notification bot renders them as regular text messages in the
// user's "通知助手" conversation.

func todoCreatedMsg(title, actorName string) string {
	return fmt.Sprintf("📋 新任务「%s」— %s 分配给了你", title, actorName)
}

func statusChangedMsg(title, actorName, newStatus string) string {
	action := "关闭了"
	if newStatus == "open" {
		action = "重新打开了"
	}
	return fmt.Sprintf("📋 任务「%s」— %s %s", title, actorName, action)
}

func assigneeAddedMsg(title, actorName string) string {
	return fmt.Sprintf("📋 任务「%s」— %s 将你添加为负责人", title, actorName)
}

func commentAddedMsg(title, actorName string) string {
	return fmt.Sprintf("📋 任务「%s」— %s 添加了评论", title, actorName)
}
