// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package model

import "time"

// TimelineAttachment is a file reference owned by a single timeline entry. Its
// lifecycle is bound to that entry through ON DELETE CASCADE.
type TimelineAttachment struct {
	ID        string    `db:"id" json:"id"`
	EntryID   string    `db:"entry_id" json:"entry_id"`
	FileURL   string    `db:"file_url" json:"file_url"`
	FileName  *string   `db:"file_name" json:"file_name,omitempty"`
	FileSize  *int64    `db:"file_size" json:"file_size,omitempty"`
	MimeType  *string   `db:"mime_type" json:"mime_type,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}
