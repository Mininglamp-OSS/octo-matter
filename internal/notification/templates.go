// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package notification

import "fmt"

func matterCreatedMsg(title, actorName string) string {
	return fmt.Sprintf("📋 新任务「%s」— %s 分配给了你", title, actorName)
}

func statusChangedMsg(title, actorName, newStatus string) string {
	var action string
	switch newStatus {
	case "done":
		action = "完成了"
	case "archived":
		action = "归档了"
	case "open":
		action = "重新打开了"
	default:
		action = "更新了"
	}
	return fmt.Sprintf("📋 任务「%s」— %s %s", title, actorName, action)
}

func assigneeAddedMsg(title, actorName string) string {
	return fmt.Sprintf("📋 任务「%s」— %s 将你添加为负责人", title, actorName)
}

func timelineEntryAddedMsg(title, actorName string) string {
	return fmt.Sprintf("📋 任务「%s」— %s 添加了进展", title, actorName)
}
