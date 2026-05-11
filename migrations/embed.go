// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package migrations

import "embed"

//go:embed 001_init.sql 002_rename_to_matters.sql 003_permissions_upgrade.sql 004_digest_and_activities.sql 005_unify_timeline.sql 006_matter_source_msg_ids.sql
var FS embed.FS
