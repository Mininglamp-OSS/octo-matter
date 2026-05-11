package main

import (
	"context"

	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
)

// timelineTxAdapter wires service.TimelineService's tx abstraction to the
// dbr-backed repository.TxManager. Kept in cmd/ to avoid making the service
// package import repository just for a tx shape.
type timelineTxAdapter struct {
	mgr *repository.TxManager
}

func (a timelineTxAdapter) Do(ctx context.Context, fn func(service.TimelineStore, service.TimelineAttachmentStore, service.ParticipantUpserter, service.TimelineMatterChannelStore) error) error {
	return a.mgr.Do(ctx, func(r *repository.TxRepos) error {
		return fn(r.Timeline, r.TimelineAttachment, r.Participant, r.MatterChannel)
	})
}
