package model

import "testing"

func TestIsValidStatus(t *testing.T) {
	if !IsValidStatus(MatterStatusOpen) {
		t.Error("open should be valid")
	}
	if !IsValidStatus(MatterStatusDone) {
		t.Error("done should be valid")
	}
	if !IsValidStatus(MatterStatusArchived) {
		t.Error("archived should be valid")
	}
	if IsValidStatus("closed") {
		t.Error("closed should be invalid")
	}
	if IsValidStatus("draft") {
		t.Error("draft should be invalid")
	}
	if IsValidStatus("banana") {
		t.Error("banana should be invalid")
	}
}
