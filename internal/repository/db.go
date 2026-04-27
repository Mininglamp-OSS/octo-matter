package repository

import (
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gocraft/dbr/v2"
)

// NewSession creates a MySQL dbr connection and returns the underlying
// connection plus a session bound to it. The connection is exposed so main.go
// can wire a readiness probe without reaching through the session.
func NewSession(dsn string) (*dbr.Connection, *dbr.Session, error) {
	conn, err := dbr.Open("mysql", dsn, nil)
	if err != nil {
		return nil, nil, err
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)
	if err := conn.Ping(); err != nil {
		return nil, nil, err
	}
	return conn, conn.NewSession(nil), nil
}

// isDuplicateKeyErr reports whether err is a MySQL duplicate-key violation (1062).
func isDuplicateKeyErr(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	return false
}

