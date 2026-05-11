// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"fmt"
	"net/http"

	"github.com/Mininglamp-OSS/octo-matter/migrations"
	"github.com/gocraft/dbr/v2"
	migrate "github.com/rubenv/sql-migrate"
)

// RunMigrations applies all pending database migrations embedded in the binary.
func RunMigrations(conn *dbr.Connection) (int, error) {
	source := &migrate.HttpFileSystemMigrationSource{
		FileSystem: http.FS(migrations.FS),
	}
	n, err := migrate.Exec(conn.DB, "mysql", source, migrate.Up)
	if err != nil {
		return 0, fmt.Errorf("migration failed: %w", err)
	}
	return n, nil
}
