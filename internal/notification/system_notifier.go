package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// SystemNotifier sends notifications via WuKongIM /message/send API
// using the system admin UID (u_10000), same pattern as dmworkim internal
// message sending. No Robot registration or app_key needed.
type SystemNotifier struct {
	wkimURL  string // WuKongIM API URL
	fromUID  string // system admin UID (u_10000)
	client   *http.Client
}

func NewSystemNotifier(wkimURL string) *SystemNotifier {
	return &SystemNotifier{
		wkimURL: wkimURL,
		fromUID: "u_10000",
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *SystemNotifier) sendToUser(toUID, content string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"type":    1,
		"content": content,
	})

	body, _ := json.Marshal(map[string]interface{}{
		"header": map[string]interface{}{
			"no_persist": 0,
			"red_dot":    1,
			"sync_once":  0,
		},
		"from_uid":     n.fromUID,
		"channel_id":   toUID,
		"channel_type": 1, // personal channel
		"payload":      payload,
	})

	resp, err := n.client.Post(n.wkimURL+"/message/send", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("wkim send failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wkim send returned %d", resp.StatusCode)
	}
	return nil
}

func (n *SystemNotifier) notify(actorID string, targetIDs []string, content string) {
	seen := make(map[string]bool)
	for _, uid := range targetIDs {
		if uid == actorID || uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		if err := n.sendToUser(uid, content); err != nil {
			log.Printf("WARN: notification send failed: uid=%s err=%v", uid, err)
		}
	}
}

func (n *SystemNotifier) NotifyTodoCreated(todo *model.Todo, actorName string, assigneeIDs []string) {
	n.notify(todo.CreatorID, assigneeIDs, todoCreatedMsg(todo.Title, actorName))
}

func (n *SystemNotifier) NotifyStatusChanged(todo *model.Todo, actorID, actorName string, assigneeIDs []string) {
	targets := append([]string{todo.CreatorID}, assigneeIDs...)
	n.notify(actorID, targets, statusChangedMsg(todo.Title, actorName, string(todo.Status)))
}

func (n *SystemNotifier) NotifyAssigneeAdded(todo *model.Todo, actorName, newAssigneeID string) {
	n.notify("", []string{newAssigneeID}, assigneeAddedMsg(todo.Title, actorName))
}

func (n *SystemNotifier) NotifyCommentAdded(todo *model.Todo, actorID, actorName string, assigneeIDs []string) {
	targets := append([]string{todo.CreatorID}, assigneeIDs...)
	n.notify(actorID, targets, commentAddedMsg(todo.Title, actorName))
}
