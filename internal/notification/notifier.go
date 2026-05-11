// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

// Package notification defines the Notifier interface for system notifications.
package notification

import (
	"log"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// Notifier sends system notifications for matter events.
type Notifier interface {
	// NotifyMatterCreated tells each assignee that a new matter was created.
	NotifyMatterCreated(matter *model.Matter, actorName string, assigneeIDs []string)

	// NotifyStatusChanged tells assignees + creator + participants that status changed.
	NotifyStatusChanged(matter *model.Matter, actorID, actorName string, assigneeIDs, participantIDs []string)

	// NotifyAssigneeAdded tells the new assignee they were added.
	NotifyAssigneeAdded(matter *model.Matter, actorName, newAssigneeID string)

	// NotifyTimelineEntryAdded tells assignees + creator + participants about a new timeline entry.
	NotifyTimelineEntryAdded(matter *model.Matter, actorID, actorName string, assigneeIDs, participantIDs []string)
}

// Noop is a no-op Notifier used in tests or when notifications are disabled.
type Noop struct{}

func (Noop) NotifyMatterCreated(_ *model.Matter, _ string, _ []string)            {}
func (Noop) NotifyStatusChanged(_ *model.Matter, _, _ string, _, _ []string)      {}
func (Noop) NotifyAssigneeAdded(_ *model.Matter, _, _ string)                     {}
func (Noop) NotifyTimelineEntryAdded(_ *model.Matter, _, _ string, _, _ []string) {}

// SafeGo runs fn in a goroutine with panic recovery.
func SafeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ERROR: notification panic: %v", r)
			}
		}()
		fn()
	}()
}
