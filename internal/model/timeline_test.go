// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"reflect"
	"testing"
)

// TestJSONStringSlice_Value_ReturnsString guards against a regression where
// Value returned []byte. The MySQL driver sends []byte with the "binary"
// charset, which MySQL refuses to coerce into a JSON column, producing
// "Cannot create a JSON value from a string with CHARACTER SET 'binary'".
// Returning string forces a text-charset bind and works against JSON columns.
func TestJSONStringSlice_Value_ReturnsString(t *testing.T) {
	s := JSONStringSlice{"a", "b"}
	v, err := s.Value()
	if err != nil {
		t.Fatalf("Value: unexpected err: %v", err)
	}
	got, ok := v.(string)
	if !ok {
		t.Fatalf("Value must return string for MySQL JSON column, got %T", v)
	}
	if got != `["a","b"]` {
		t.Errorf("unexpected payload: %q", got)
	}
}

func TestJSONStringSlice_Value_NilIsSQLNull(t *testing.T) {
	var s JSONStringSlice
	v, err := s.Value()
	if err != nil {
		t.Fatalf("Value: unexpected err: %v", err)
	}
	if v != nil {
		t.Errorf("nil slice must serialize to SQL NULL, got %#v", v)
	}
}

func TestJSONStringSlice_Value_Empty(t *testing.T) {
	s := JSONStringSlice{}
	v, err := s.Value()
	if err != nil {
		t.Fatalf("Value: unexpected err: %v", err)
	}
	if v.(string) != `[]` {
		t.Errorf("empty slice must serialize to []: got %q", v)
	}
}

func TestJSONStringSlice_RoundTrip(t *testing.T) {
	cases := []JSONStringSlice{
		nil,
		{},
		{"single"},
		{"uid_a", "uid_b", "uid_c"},
		{"with \"quotes\" and 中文"},
	}
	for _, in := range cases {
		v, err := in.Value()
		if err != nil {
			t.Fatalf("Value(%v): %v", in, err)
		}

		var out JSONStringSlice
		// Driver delivers []byte on Scan; mimic that.
		var src interface{}
		switch tv := v.(type) {
		case nil:
			src = nil
		case string:
			src = []byte(tv)
		}
		if err := out.Scan(src); err != nil {
			t.Fatalf("Scan(%v): %v", v, err)
		}
		if len(in) == 0 && len(out) == 0 {
			continue
		}
		if !reflect.DeepEqual([]string(in), []string(out)) {
			t.Errorf("round trip mismatch: in=%v out=%v", in, out)
		}
	}
}
