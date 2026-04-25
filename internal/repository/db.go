package repository

import (
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gocraft/dbr/v2"
)

// NewSession creates a MySQL dbr connection and returns a session.
func NewSession(dsn string) (*dbr.Session, error) {
	conn, err := dbr.Open("mysql", dsn, nil)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(5)
	if err := conn.Ping(); err != nil {
		return nil, err
	}
	return conn.NewSession(nil), nil
}

// placeholder for dbr.I usage in conditions
var _ = sql.ErrNoRows
