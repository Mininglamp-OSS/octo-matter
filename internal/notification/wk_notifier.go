package notification

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// WKNotifier sends notifications via WuKongIM's /message/send API, appearing
// in the user's "通知助手" conversation. It is safe for concurrent use.
type WKNotifier struct {
	apiURL string
	botUID string
	client *http.Client
}

// NewWKNotifier creates a notifier that sends messages through WuKongIM.
// apiURL is the WuKongIM HTTP API base (e.g. "http://wukongim:5001").
// botUID is the system notification bot UID (e.g. "notification").
func NewWKNotifier(apiURL, botUID string) *WKNotifier {
	return &WKNotifier{
		apiURL: apiURL,
		botUID: botUID,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// sendToUser sends a text message from the notification bot to a single user.
func (n *WKNotifier) sendToUser(toUID, content string) error {
	// WuKongIM expects payload as base64-encoded JSON string.
	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"type":    1, // text message
		"content": content,
	})
	payloadB64 := base64.StdEncoding.EncodeToString(payloadJSON)

	body, _ := json.Marshal(map[string]interface{}{
		"header": map[string]interface{}{
			"no_persist": 0,
			"red_dot":    1,
			"sync_once":  0,
		},
		"from_uid":     n.botUID,
		"channel_id":   toUID,
		"channel_type": 1, // personal channel
		"payload":      payloadB64,
	})

	resp, err := n.client.Post(n.apiURL+"/message/send", "application/json", bytes.NewReader(body))
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

// notify deduplicates and sends to each target, skipping the actor.
func (n *WKNotifier) notify(actorID string, targetIDs []string, content string) {
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

func (n *WKNotifier) NotifyTodoCreated(todo *model.Todo, actorName string, assigneeIDs []string) {
	msg := todoCreatedMsg(todo.Title, actorName)
	n.notify(todo.CreatorID, assigneeIDs, msg)
}

func (n *WKNotifier) NotifyStatusChanged(todo *model.Todo, actorID, actorName string, assigneeIDs []string) {
	msg := statusChangedMsg(todo.Title, actorName, string(todo.Status))
	// Notify creator + all assignees; actorID is excluded by notify().
	targets := make([]string, 0, 1+len(assigneeIDs))
	targets = append(targets, todo.CreatorID)
	targets = append(targets, assigneeIDs...)
	n.notify(actorID, targets, msg)
}

func (n *WKNotifier) NotifyAssigneeAdded(todo *model.Todo, actorName, newAssigneeID string) {
	msg := assigneeAddedMsg(todo.Title, actorName)
	n.notify("", []string{newAssigneeID}, msg)
}

func (n *WKNotifier) NotifyCommentAdded(todo *model.Todo, actorID, actorName string, assigneeIDs []string) {
	msg := commentAddedMsg(todo.Title, actorName)
	targets := make([]string, 0, 1+len(assigneeIDs))
	targets = append(targets, todo.CreatorID)
	targets = append(targets, assigneeIDs...)
	n.notify(actorID, targets, msg)
}
