package notification

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// OctoNotifier sends notifications via the octoim internal notify API
// (POST {OctoIMURL}/v1/internal/notify). Authentication is via the
// X-Internal-Token header; requests are fire-and-forget.
type OctoNotifier struct {
	baseURL string
	token   string
	// defaultLang renders the payload's fallback `message` string. Per-recipient
	// localization is the IM server's job (from message_key + params); this is
	// only a safety net for clients/IM that have not adopted the key.
	defaultLang string
	client      *http.Client
}

const (
	notifyService = "matter-service"

	eventMatterCreated      = "matter.created"
	eventStatusChanged      = "matter.status_changed"
	eventAssigneeAdded      = "matter.assignee_added"
	eventTimelineEntryAdded = "matter.timeline_entry_added"
)

type notifyRequest struct {
	SpaceID  string                 `json:"space_id"`
	Service  string                 `json:"service"`
	Event    string                 `json:"event"`
	Targets  []string               `json:"targets"`
	ActorUID string                 `json:"actor_uid,omitempty"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}

func NewOctoNotifier(octoIMURL, internalToken, defaultLang string) *OctoNotifier {
	if defaultLang == "" {
		defaultLang = i18n.DefaultLanguage()
	}
	return &OctoNotifier{
		baseURL:     octoIMURL,
		token:       internalToken,
		defaultLang: defaultLang,
		client:      &http.Client{Timeout: 10 * time.Second},
	}
}

// dedupTargets returns uids with empties, duplicates, and the actor removed.
func dedupTargets(actorID string, uids []string) []string {
	seen := make(map[string]bool, len(uids))
	out := make([]string, 0, len(uids))
	for _, uid := range uids {
		if uid == "" || uid == actorID || seen[uid] {
			continue
		}
		seen[uid] = true
		out = append(out, uid)
	}
	return out
}

func (n *OctoNotifier) send(spaceID, event, actorID string, targets []string, payload map[string]interface{}) {
	if spaceID == "" || len(targets) == 0 {
		return
	}
	body, err := json.Marshal(notifyRequest{
		SpaceID:  spaceID,
		Service:  notifyService,
		Event:    event,
		Targets:  targets,
		ActorUID: actorID,
		Payload:  payload,
	})
	if err != nil {
		log.Printf("WARN: notify marshal failed: event=%s err=%v", event, err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, n.baseURL+"/v1/internal/notify", bytes.NewReader(body))
	if err != nil {
		log.Printf("WARN: notify request build failed: event=%s err=%v", event, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if n.token != "" {
		req.Header.Set("X-Internal-Token", n.token)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("WARN: notify send failed: event=%s err=%v", event, err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("WARN: notify returned %d: event=%s", resp.StatusCode, event)
	}
}

// payloadFor builds the notify payload. It carries the structured message_key
// + params so the IM server can localize per recipient, plus a default-language
// `message` fallback. extra merges event-specific fields (e.g. action_key).
func (n *OctoNotifier) payloadFor(matter *model.Matter, messageKey string, params, extra map[string]any) map[string]interface{} {
	p := map[string]interface{}{
		"matter_id":    matter.ID,
		"matter_title": matter.Title,
		"message_key":  messageKey,
		"params":       params,
		"message":      i18n.Localize(n.defaultLang, messageKey, params),
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func (n *OctoNotifier) NotifyMatterCreated(matter *model.Matter, actorName string, assigneeIDs []string) {
	targets := dedupTargets(matter.CreatorID, assigneeIDs)
	params := map[string]any{"Title": matter.Title, "Actor": actorName}
	n.send(matter.SpaceID, eventMatterCreated, matter.CreatorID, targets,
		n.payloadFor(matter, i18n.KeyNotifyMatterCreated, params, nil))
}

func (n *OctoNotifier) NotifyStatusChanged(matter *model.Matter, actorID, actorName string, assigneeIDs, participantIDs []string) {
	all := append([]string{matter.CreatorID}, assigneeIDs...)
	all = append(all, participantIDs...)
	targets := dedupTargets(actorID, all)
	aKey := actionKey(string(matter.Status))
	params := map[string]any{
		"Title": matter.Title,
		"Actor": actorName,
		// Action is the default-language verb for the fallback message; the IM
		// server should re-localize action_key per recipient instead.
		"Action": i18n.Localize(n.defaultLang, aKey, nil),
	}
	n.send(matter.SpaceID, eventStatusChanged, actorID, targets,
		n.payloadFor(matter, i18n.KeyNotifyStatusChanged, params, map[string]any{"action_key": aKey}))
}

func (n *OctoNotifier) NotifyAssigneeAdded(matter *model.Matter, actorName, newAssigneeID string) {
	targets := dedupTargets("", []string{newAssigneeID})
	params := map[string]any{"Title": matter.Title, "Actor": actorName}
	n.send(matter.SpaceID, eventAssigneeAdded, "", targets,
		n.payloadFor(matter, i18n.KeyNotifyAssigneeAdded, params, nil))
}

func (n *OctoNotifier) NotifyTimelineEntryAdded(matter *model.Matter, actorID, actorName string, assigneeIDs, participantIDs []string) {
	all := append([]string{matter.CreatorID}, assigneeIDs...)
	all = append(all, participantIDs...)
	targets := dedupTargets(actorID, all)
	params := map[string]any{"Title": matter.Title, "Actor": actorName}
	n.send(matter.SpaceID, eventTimelineEntryAdded, actorID, targets,
		n.payloadFor(matter, i18n.KeyNotifyTimelineEntryAdded, params, nil))
}

// static check that OctoNotifier satisfies Notifier
var _ Notifier = (*OctoNotifier)(nil)
