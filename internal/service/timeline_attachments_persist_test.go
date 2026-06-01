package service

import (
	"context"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// TestCreateFromMessages_PersistsAttachments is the end-to-end guard that
// msgs[].attachments survive the LLM path and land in matter_timeline_attachments
// with matter_id + sender + sent_at populated — so the matter outputs view can
// serve them.
func TestCreateFromMessages_PersistsAttachments(t *testing.T) {
	llmStub := &stubLLM{out: `{"content":"summary","source_msg_ids":["m_a"]}`}
	repo := newFakeTimelineRepo()
	channels := &trackingChannelRepoLLM{}
	svc := newLLMTimelineSvc(t, llmStub, repo, channels)
	// Reach into the service to grab the same attachment repo the svc holds —
	// newLLMTimelineSvc constructs a fresh one and stores it on the service.
	attRepo, ok := svc.attachmentRepo.(*fakeTimelineAttachmentRepo)
	if !ok {
		t.Fatalf("expected fake attachment repo, got %T", svc.attachmentRepo)
	}
	// The tx fake uses the repo it was wired with, which is the same instance.
	// (See newLLMTimelineSvc — both the service and the tx receive attRepo.)

	in := baseLLMInput()
	in.Messages = []ExtractMessage{
		{
			MessageID: "m_a", FromUID: "u_sender", FromUname: "Sender", Timestamp: 1779344220,
			Content: "[file]",
			Attachments: []ExtractMessageAttachment{{
				FileName: "doc.pdf",
				FileURL:  "https://cdn.example/doc.pdf",
			}},
		},
		// Uncited message — its attachment must NOT be persisted.
		{
			MessageID: "m_b", FromUID: "u_other", Timestamp: 1779344300,
			Attachments: []ExtractMessageAttachment{{FileURL: "https://cdn.example/other.pdf"}},
		},
	}

	entry, _, err := svc.CreateEntry(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if entry == nil {
		t.Fatal("nil entry")
	}

	got := attRepo.byEntryID[entry.ID]
	if len(got) != 1 {
		t.Fatalf("expected exactly one persisted attachment (cited), got %d", len(got))
	}
	a := got[0]
	if a.MatterID != in.MatterID {
		t.Errorf("matter_id missing: %+v", a)
	}
	if a.SenderUID != "u_sender" || a.SenderUname != "Sender" {
		t.Errorf("sender fields wrong: %+v", a)
	}
	want := time.Unix(1779344220, 0).UTC()
	if !a.SentAt.Equal(want) {
		t.Errorf("sent_at mismatch: got %s, want %s", a.SentAt, want)
	}
	if a.Description == nil || *a.Description != "summary" {
		t.Errorf("description should be the LLM-extracted content: %+v", a.Description)
	}
}

// TestCreateFromMessages_DedupsAgainstExistingMatterRow guards the LLM-path
// dedup branch: if the same (matter_id, file_url) already exists from an
// earlier timeline entry, the second insert is skipped — so the matter
// outputs view stays one row per file.
func TestCreateFromMessages_DedupsAgainstExistingMatterRow(t *testing.T) {
	llmStub := &stubLLM{out: `{"content":"x","source_msg_ids":["m_a"]}`}
	repo := newFakeTimelineRepo()
	svc := newLLMTimelineSvc(t, llmStub, repo, &trackingChannelRepoLLM{})
	attRepo := svc.attachmentRepo.(*fakeTimelineAttachmentRepo)

	// Seed: file already attached to the same matter via a prior entry.
	priorName := "doc.pdf"
	attRepo.byEntryID["prior-entry"] = []*model.TimelineAttachment{{
		ID:        "prior-att",
		EntryID:   "prior-entry",
		MatterID:  "m1",
		FileURL:   "https://cdn.example/doc.pdf",
		FileName:  &priorName,
		SenderUID: "u_orig",
	}}

	in := baseLLMInput()
	in.Messages = []ExtractMessage{{
		MessageID: "m_a", FromUID: "u_forwarder", Timestamp: 1779344300,
		Attachments: []ExtractMessageAttachment{{FileName: "doc.pdf", FileURL: "https://cdn.example/doc.pdf"}},
	}}

	if _, _, err := svc.CreateEntry(context.Background(), in); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// The prior entry's attachment stays; the new entry MUST NOT get a row
	// for the same file_url.
	for entryID, atts := range attRepo.byEntryID {
		if entryID == "prior-entry" {
			continue
		}
		for _, a := range atts {
			if a.FileURL == "https://cdn.example/doc.pdf" {
				t.Fatalf("duplicate file_url persisted on a new entry: %+v", a)
			}
		}
	}
}
