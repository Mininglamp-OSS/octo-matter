// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
	"time"
)

// JSONDetail is a nullable raw JSON column. database/sql's default convert
// path refuses to scan SQL NULL into json.RawMessage (it errors with
// "unsupported Scan, storing driver.Value type <nil>"), so we implement
// Scanner explicitly. MarshalJSON renders an empty value as JSON null,
// matching the wire contract for activities with no detail payload.
type JSONDetail []byte

func (d *JSONDetail) Scan(src interface{}) error {
	if src == nil {
		*d = nil
		return nil
	}
	switch v := src.(type) {
	case []byte:
		buf := make([]byte, len(v))
		copy(buf, v)
		*d = buf
		return nil
	case string:
		*d = []byte(v)
		return nil
	}
	return fmt.Errorf("JSONDetail: unsupported scan type %T", src)
}

func (d JSONDetail) MarshalJSON() ([]byte, error) {
	if len(d) == 0 {
		return []byte("null"), nil
	}
	return []byte(d), nil
}

// MatterActivity records a mutation on a matter.
//
// The Detail field is a JSON object whose shape depends on Action:
//
//	created:             {}
//	title_changed:       {"from": string, "to": string}
//	description_changed: {"summary": string}
//	deadline_changed:    {"from": int|null, "to": int|null}  (unix seconds)
//	status_changed:      {"from": string, "to": string}
//	assignee_added:      {"user_id": string}
//	assignee_removed:    {"user_id": string}
//	channel_linked:      {"channel_id": string, "channel_name"?: string}
//	channel_unlinked:    {"channel_id": string}
type MatterActivity struct {
	ID        string          `db:"id"         json:"id"`
	MatterID  string          `db:"matter_id"  json:"matter_id"`
	ActorID   string          `db:"actor_id"   json:"actor_id"`
	Action    string          `db:"action"     json:"action"`
	Detail    JSONDetail      `db:"detail"     json:"detail"`
	CreatedAt time.Time       `db:"created_at" json:"created_at"`
}
