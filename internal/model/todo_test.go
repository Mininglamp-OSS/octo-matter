package model

import "testing"

func TestIsValidStatus(t *testing.T) {
	if !IsValidStatus(TodoStatusOpen) {
		t.Error("open should be valid")
	}
	if !IsValidStatus(TodoStatusClosed) {
		t.Error("closed should be valid")
	}
	if IsValidStatus("draft") {
		t.Error("draft should be invalid")
	}
	if IsValidStatus("banana") {
		t.Error("banana should be invalid")
	}
}
