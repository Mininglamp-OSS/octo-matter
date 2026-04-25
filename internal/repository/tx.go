package repository

import (
	"github.com/gocraft/dbr/v2"
)

// TxRepos bundles every repo bound to the same transaction. The TxManager
// builds one per Do() call; services use the fields directly inside the
// closure. Adding a new repo to the service layer means adding a field here.
type TxRepos struct {
	Todo       *TodoRepo
	Goal       *GoalRepo
	Assignee   *AssigneeRepo
	Comment    *CommentRepo
	Attachment *AttachmentRepo
}

// TxManager coordinates writes that must succeed or fail atomically.
// Transactional scope lives at the service layer: a service calls Do() and
// operates on the tx-bound repos inside the closure. Outside the closure
// services keep using their normal (session-bound) repos for read paths.
type TxManager struct {
	sess *dbr.Session
}

func NewTxManager(sess *dbr.Session) *TxManager {
	return &TxManager{sess: sess}
}

// Do runs fn inside a dbr transaction. Any error from fn (or from Commit)
// causes a rollback; panics rollback too. The repos handed to fn share the
// same tx; mutations outside the callback are NOT covered.
func (m *TxManager) Do(fn func(r *TxRepos) error) error {
	tx, err := m.sess.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	repos := &TxRepos{
		Todo:       &TodoRepo{runner: tx},
		Goal:       &GoalRepo{runner: tx},
		Assignee:   &AssigneeRepo{runner: tx},
		Comment:    &CommentRepo{runner: tx},
		Attachment: &AttachmentRepo{runner: tx},
	}
	if err := fn(repos); err != nil {
		return err
	}
	return tx.Commit()
}
