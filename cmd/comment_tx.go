package main

import (
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/Mininglamp-OSS/octo-matter/internal/service"
)

// commentTxAdapter wires service.CommentService's tx abstraction to the
// dbr-backed repository.TxManager. Kept in cmd/ to avoid making the service
// package import repository just for a tx shape.
type commentTxAdapter struct {
	mgr *repository.TxManager
}

func (a commentTxAdapter) Do(fn func(service.CommentStore, service.CommentAttachmentStore, service.ParticipantUpserter) error) error {
	return a.mgr.Do(func(r *repository.TxRepos) error {
		return fn(r.Comment, r.CommentAttachment, r.Participant)
	})
}
