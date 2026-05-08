package main

import (
	"context"

	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
)

// commentTxAdapter wires service.CommentService's tx abstraction to the
// dbr-backed repository.TxManager. Kept in cmd/ to avoid making the service
// package import repository just for a tx shape.
type commentTxAdapter struct {
	mgr *repository.TxManager
}

func (a commentTxAdapter) Do(ctx context.Context, fn func(service.CommentStore, service.CommentAttachmentStore, service.ParticipantUpserter) error) error {
	return a.mgr.Do(ctx, func(r *repository.TxRepos) error {
		return fn(r.Comment, r.CommentAttachment, r.Participant)
	})
}
