package repository

import (
	"github.com/gocraft/dbr/v2"
)

// TxRepos bundles every repo bound to the same transaction.
type TxRepos struct {
	Matter            *MatterRepo
	Assignee          *AssigneeRepo
	Comment           *CommentRepo
	CommentAttachment *CommentAttachmentRepo
}

// TxManager coordinates writes that must succeed or fail atomically.
type TxManager struct {
	sess *dbr.Session
}

func NewTxManager(sess *dbr.Session) *TxManager {
	return &TxManager{sess: sess}
}

// Do runs fn inside a dbr transaction.
func (m *TxManager) Do(fn func(r *TxRepos) error) error {
	tx, err := m.sess.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	repos := &TxRepos{
		Matter:            &MatterRepo{runner: tx},
		Assignee:          &AssigneeRepo{runner: tx},
		Comment:           &CommentRepo{runner: tx},
		CommentAttachment: &CommentAttachmentRepo{runner: tx},
	}
	if err := fn(repos); err != nil {
		return err
	}
	return tx.Commit()
}
