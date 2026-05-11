// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// TestMatterDetail_GetMatter_ExposesSourceMsgs is the service-to-wire test
// that PR #43 missed and PR #44 partially fixed: GET /api/v1/matters/:id must
// emit `source_msgs` in the response. The MatterDetail DTO embeds
// *model.Matter, so any drift in the Matter JSON tag would silently hide the
// field. The wire name matches TimelineEntry.SourceMsgs to keep one name for
// the same concept across endpoints.
func TestMatterDetail_GetMatter_ExposesSourceMsgs(t *testing.T) {
	matter := &model.Matter{
		ID:           "t1",
		SpaceID:      "sp1",
		CreatorID:    "owner",
		Status:       model.MatterStatusOpen,
		SourceMsgIDs: model.JSONStringSlice{"msg-a", "msg-b"},
	}
	matterRepo := &hasAccessFakeMatterRepo{
		fakeMatterRepo: *newFakeMatterRepo(matter),
		accessGrants:   map[string]bool{"t1:owner:": true},
	}
	svc := NewMatterService(matterRepo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, nil)

	detail, err := svc.GetMatter(context.Background(), "t1", "sp1", []string{"owner"}, "", "")
	if err != nil {
		t.Fatalf("GetMatter: %v", err)
	}
	b, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"source_msgs":["msg-a","msg-b"]`) {
		t.Fatalf("MatterDetail JSON missing populated source_msgs; got %s", b)
	}
	// Guard against a future "compat double-emit" regression that ships
	// both names — clients would diverge on which one to read.
	if strings.Contains(string(b), `"source_msg_ids"`) {
		t.Fatalf("MatterDetail JSON must NOT also emit legacy source_msg_ids; got %s", b)
	}
}

// TestMatterDetail_GetMatter_LegacyMatterStillExposesEmptyArray ensures rows
// predating migration 006 (nil SourceMsgIDs) still emit the field as an empty
// JSON array so the wire contract stays stable across data eras.
func TestMatterDetail_GetMatter_LegacyMatterStillExposesEmptyArray(t *testing.T) {
	matter := &model.Matter{
		ID:        "t-legacy",
		SpaceID:   "sp1",
		CreatorID: "owner",
		Status:    model.MatterStatusOpen,
		// SourceMsgIDs intentionally nil — legacy row.
	}
	matterRepo := &hasAccessFakeMatterRepo{
		fakeMatterRepo: *newFakeMatterRepo(matter),
		accessGrants:   map[string]bool{"t-legacy:owner:": true},
	}
	svc := NewMatterService(matterRepo, newFakeAssigneeRepo(), fakeParticipantRepo{}, fakeChannelRepo{}, nil, noopTxRunner{}, nil)

	detail, err := svc.GetMatter(context.Background(), "t-legacy", "sp1", []string{"owner"}, "", "")
	if err != nil {
		t.Fatalf("GetMatter: %v", err)
	}
	b, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"source_msgs":[]`) {
		t.Fatalf("legacy matter JSON must render source_msgs as []; got %s", b)
	}
	if strings.Contains(string(b), `"source_msg_ids"`) {
		t.Fatalf("legacy matter JSON must NOT also emit legacy source_msg_ids; got %s", b)
	}
}
