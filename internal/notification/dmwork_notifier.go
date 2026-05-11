// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package notification

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// DmworkNotifier sends notifications via the dmworkim internal notify API
// (POST {DmworkIMURL}/v1/internal/notify). Authentication is via the
// X-Internal-Token header; requests are fire-and-forget.
type DmworkNotifier struct {
	baseURL string
	token   string
	client  *http.Client
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

func NewDmworkNotifier(dmworkIMURL, internalToken string) *DmworkNotifier {
	return &DmworkNotifier{
		baseURL: dmworkIMURL,
		token:   internalToken,
		client:  &http.Client{Timeout: 10 * time.Second},
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

func (n *DmworkNotifier) send(spaceID, event, actorID string, targets []string, payload map[string]interface{}) {
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

func payloadFor(matter *model.Matter, message string) map[string]interface{} {
	return map[string]interface{}{
		"matter_id":    matter.ID,
		"matter_title": matter.Title,
		"message":      message,
	}
}

func (n *DmworkNotifier) NotifyMatterCreated(matter *model.Matter, actorName string, assigneeIDs []string) {
	targets := dedupTargets(matter.CreatorID, assigneeIDs)
	n.send(matter.SpaceID, eventMatterCreated, matter.CreatorID, targets,
		payloadFor(matter, matterCreatedMsg(matter.Title, actorName)))
}

func (n *DmworkNotifier) NotifyStatusChanged(matter *model.Matter, actorID, actorName string, assigneeIDs, participantIDs []string) {
	all := append([]string{matter.CreatorID}, assigneeIDs...)
	all = append(all, participantIDs...)
	targets := dedupTargets(actorID, all)
	n.send(matter.SpaceID, eventStatusChanged, actorID, targets,
		payloadFor(matter, statusChangedMsg(matter.Title, actorName, string(matter.Status))))
}

func (n *DmworkNotifier) NotifyAssigneeAdded(matter *model.Matter, actorName, newAssigneeID string) {
	targets := dedupTargets("", []string{newAssigneeID})
	n.send(matter.SpaceID, eventAssigneeAdded, "", targets,
		payloadFor(matter, assigneeAddedMsg(matter.Title, actorName)))
}

func (n *DmworkNotifier) NotifyTimelineEntryAdded(matter *model.Matter, actorID, actorName string, assigneeIDs, participantIDs []string) {
	all := append([]string{matter.CreatorID}, assigneeIDs...)
	all = append(all, participantIDs...)
	targets := dedupTargets(actorID, all)
	n.send(matter.SpaceID, eventTimelineEntryAdded, actorID, targets,
		payloadFor(matter, timelineEntryAddedMsg(matter.Title, actorName)))
}

// static check that DmworkNotifier satisfies Notifier
var _ Notifier = (*DmworkNotifier)(nil)
