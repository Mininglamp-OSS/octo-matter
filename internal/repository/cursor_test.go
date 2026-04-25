package repository

import (
	"errors"
	"testing"
	"time"
)

func TestCursor_RoundTrip(t *testing.T) {
	in := Cursor{
		CreatedAt: time.Date(2026, 4, 25, 14, 30, 45, 123456789, time.UTC),
		ID:        "abc-123",
	}
	s := EncodeCursor(in)
	if s == "" {
		t.Fatal("EncodeCursor returned empty string")
	}
	out, err := DecodeCursor(s)
	if err != nil {
		t.Fatalf("DecodeCursor failed: %v", err)
	}
	if !out.CreatedAt.Equal(in.CreatedAt) || out.ID != in.ID {
		t.Errorf("round trip mismatch: in=%+v out=%+v", in, out)
	}
}

func TestDecodeCursor_Rejects(t *testing.T) {
	cases := []string{
		"",
		"not-base64!",
		"Zm9v",        // valid base64 but no "|"
		"fG5vLXRz",    // "|no-ts" — empty nano part, parse fails
		"MTIzNDU2Nnw=", // "1234566|" — empty id
	}
	for _, s := range cases {
		if _, err := DecodeCursor(s); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("DecodeCursor(%q): got %v, want ErrInvalidCursor", s, err)
		}
	}
}
