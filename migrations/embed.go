package migrations

import "embed"

//go:embed 001_init.sql 002_rename_to_matters.sql 003_permissions_upgrade.sql
var FS embed.FS
