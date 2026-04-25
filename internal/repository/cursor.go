package repository

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidCursor indicates a malformed pagination cursor. Callers should
// treat it as a 400-class error; corrupting a cursor never reveals data.
var ErrInvalidCursor = errors.New("invalid cursor")

// Cursor is the composite pagination position used by ListBySpace. Ordering
// is by (created_at DESC, id DESC); the id break-tie is required because
// created_at is stored at second-or-microsecond resolution and collisions
// at the page boundary would otherwise skip or repeat rows.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// EncodeCursor serializes a Cursor to an opaque URL-safe string. Format is
// base64("<unix-nano>|<uuid>"); clients must treat it as opaque.
func EncodeCursor(c Cursor) string {
	raw := fmt.Sprintf("%d|%s", c.CreatedAt.UnixNano(), c.ID)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses the string produced by EncodeCursor. Returns
// ErrInvalidCursor if the input is not a cursor we emitted — callers should
// reject the request rather than silently falling back to "no cursor".
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, ErrInvalidCursor
	}
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return Cursor{}, ErrInvalidCursor
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{
		CreatedAt: time.Unix(0, ns),
		ID:        parts[1],
	}, nil
}
