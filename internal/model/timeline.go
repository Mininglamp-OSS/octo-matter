package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// JSONStringSlice stores a JSON array of strings in a nullable JSON column.
// It serializes as a real JSON array (not as a quoted string) and scans
// transparently from MySQL's JSON type.
type JSONStringSlice []string

// MarshalJSON emits [] for a nil slice so the API never returns `null` for
// timeline metadata fields the caller expects to iterate.
func (s JSONStringSlice) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(s))
}

// UnmarshalJSON accepts a JSON array; `null` maps to nil.
func (s *JSONStringSlice) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*s = out
	return nil
}

// Value implements driver.Valuer. Nil slice stores SQL NULL.
func (s JSONStringSlice) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	b, err := json.Marshal([]string(s))
	if err != nil {
		return nil, err
	}
	// Return as string so the MySQL driver sends it with a text charset
	// instead of "binary"; otherwise MySQL refuses to coerce into a JSON column.
	return string(b), nil
}

// Scan implements sql.Scanner.
func (s *JSONStringSlice) Scan(src interface{}) error {
	if src == nil {
		*s = nil
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return errors.New("JSONStringSlice: unsupported scan type")
	}
	if len(raw) == 0 {
		*s = nil
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*s = out
	return nil
}

// TimelineEntry represents one matter timeline entry. An entry may carry zero
// or more attachments; Attachments is populated by the service layer and is not
// a column. Content is nullable when the entry only carries attachments.
type TimelineEntry struct {
	ID              string               `db:"id" json:"id"`
	MatterID        string               `db:"matter_id" json:"matter_id"`
	UserID          string               `db:"user_id" json:"user_id"`
	Content         *string              `db:"content" json:"content"`
	ChannelID       *string              `db:"channel_id" json:"channel_id,omitempty"`
	ChannelType     *uint8               `db:"channel_type" json:"channel_type,omitempty"`
	SourceChannelID *string              `db:"source_channel_id" json:"source_channel_id,omitempty"`
	SourceMsgs      JSONStringSlice      `db:"source_msgs" json:"source_msgs,omitempty"`
	RelatedUIDs     JSONStringSlice      `db:"related_uids" json:"related_uids,omitempty"`
	CreatedAt       time.Time            `db:"created_at" json:"created_at"`
	Attachments     []TimelineAttachment `db:"-" json:"attachments"`
}
