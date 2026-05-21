//go:build integration

package service

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
	"github.com/google/uuid"
)

// wipeAllTables drops every table in the current database with FK checks
// disabled so historical/legacy tables (e.g. renamed-from migrations) don't
// block the reset. Used by both IT suites to start each test from a clean
// schema regardless of what the previous suite left behind.
func wipeAllTables(db *sql.DB) error {
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return err
	}
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, t := range tables {
		if _, err := db.Exec("DROP TABLE IF EXISTS `" + t + "`"); err != nil {
			return err
		}
	}
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS=1"); err != nil {
		return err
	}
	return nil
}

// Service-level DB integration. Boots real MySQL, runs migrations, wires the
// real repos + TxManager into TimelineService, and exercises the full
// scenario the user asked about:
//
//   1. seed a matter + channel link (the prototype's "创建事项")
//   2. call TimelineService.CreateEntry with msgs[].attachments
//      ("关联文件消息同步到事项")
//   3. call OutputsService.ListOutputs
//      ("查询产出文件")
//
// LLM is stubbed; everything else is real.
//
//   MATTER_OUTPUTS_IT_DSN='root:demo@tcp(127.0.0.1:3306)/octo_matter_outputs_it?charset=utf8mb4&parseTime=true&multiStatements=true&loc=UTC' \
//     go test -tags=integration ./internal/service/... -run Outputs_ServiceIT -v

func itDSN(t *testing.T) string {
	t.Helper()
	d := os.Getenv("MATTER_OUTPUTS_IT_DSN")
	if d == "" {
		t.Skip("MATTER_OUTPUTS_IT_DSN not set")
	}
	if !strings.Contains(d, "multiStatements=true") {
		t.Fatalf("DSN must include multiStatements=true so the migration runner can apply compound DDL files")
	}
	return d
}

// itWiring bundles everything a service-level test needs from a clean DB.
type itWiring struct {
	timelineSvc *TimelineService
	outputsSvc  *OutputsService
	llm         *stubLLM
	matterRepo  *repository.MatterRepo
	channelRepo *repository.MatterChannelRepo
}

// itTxAdapter mirrors the production cmd/timeline_tx.go shim — TimelineService
// uses a narrow tx interface so the service package never imports repository
// at compile time. Tests are free to wire the real TxManager.
type itTxAdapter struct{ mgr *repository.TxManager }

func (a itTxAdapter) Do(ctx context.Context, fn func(TimelineStore, TimelineAttachmentStore, ParticipantUpserter, TimelineMatterChannelStore) error) error {
	return a.mgr.Do(ctx, func(r *repository.TxRepos) error {
		return fn(r.Timeline, r.TimelineAttachment, r.Participant, r.MatterChannel)
	})
}

// itAccessAllow stands in for MatterService.CanAccessMatter so the service IT
// can skip the IM round-trip. The DB-level guarantees (matter/space scoping
// done by GetByID) still run.
type itAccessAllow struct{}

func (itAccessAllow) CanAccessMatter(_ context.Context, _ *model.Matter, _ []string, _, _ string) (bool, error) {
	return true, nil
}

func setupServiceIT(t *testing.T) (*itWiring, func()) {
	t.Helper()
	d := itDSN(t)

	conn, sess, err := repository.NewSession(d)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := wipeAllTables(conn.DB); err != nil {
		conn.Close()
		t.Fatalf("wipe schema: %v", err)
	}
	if _, err := repository.RunMigrations(conn); err != nil {
		conn.Close()
		t.Fatalf("migrate: %v", err)
	}

	matterRepo := repository.NewMatterRepo(sess)
	tlRepo := repository.NewTimelineRepo(sess)
	attRepo := repository.NewTimelineAttachmentRepo(sess)
	channelRepo := repository.NewMatterChannelRepo(sess)
	participantRepo := repository.NewParticipantRepo(sess)
	assigneeRepo := repository.NewAssigneeRepo(sess)
	txMgr := repository.NewTxManager(sess)

	llm := &stubLLM{}
	tlSvc := NewTimelineService(
		llm,
		tlRepo, attRepo, matterRepo,
		itAccessAllow{},
		nil, // linkVerifier — bypassed when CallerToken empty AND channel already linked; we'll seed the link
		itTxAdapter{mgr: txMgr},
		participantRepo, assigneeRepo,
		nil,
	)
	outSvc := NewOutputsService(matterRepo, itAccessAllow{}, attRepo)

	return &itWiring{
			timelineSvc: tlSvc,
			outputsSvc:  outSvc,
			llm:         llm,
			matterRepo:  matterRepo,
			channelRepo: channelRepo,
		}, func() {
			conn.Close()
		}
}

// seedMatter creates a matter and a linked channel so TimelineService can
// reach the happy path without the auto-link branch (which is gated by
// linkVerifier — out of scope for the persistence-level IT).
func (w *itWiring) seedMatter(t *testing.T, ctx context.Context, channelExtID, channelName string) string {
	t.Helper()
	m := &model.Matter{
		SpaceID:   "sp-it",
		Title:     "IT matter",
		CreatorID: "u1",
		Status:    model.MatterStatusOpen,
	}
	if err := w.matterRepo.Create(ctx, m); err != nil {
		t.Fatalf("seed matter: %v", err)
	}
	// MatterRepo.Create overwrites ID with a fresh UUID — read it back.
	chName := channelName
	if err := w.channelRepo.Create(ctx, &model.MatterChannel{
		ID:          uuid.New().String(),
		MatterID:    m.ID,
		ChannelID:   channelExtID,
		ChannelType: 2,
		ChannelName: &chName,
		LinkedBy:    "u1",
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return m.ID
}

// TestOutputs_ServiceIT_FullFlow is the scenario the user explicitly asked
// for: 创建事项 → 关联文件消息同步到事项 → 查询产出文件.
func TestOutputs_ServiceIT_FullFlow(t *testing.T) {
	w, cleanup := setupServiceIT(t)
	defer cleanup()

	ctx := context.Background()
	channelExtID := "ch-flow-" + uuid.New().String()[:8]
	matterID := w.seedMatter(t, ctx, channelExtID, "测试群")

	// Canned LLM result: cite both messages so both file attachments persist.
	w.llm.out = `{"content":"孟豪上传了发布会文稿《report.pdf》","source_msg_ids":["m_a"],"related_uids":["u_sender"]}`

	in := TimelineInput{
		MatterID:       matterID,
		SpaceID:        "sp-it",
		ActorUID:       "u_sender",
		ParticipantUID: "u_sender",
		CallerUIDs:     []string{"u_sender"},
		CallerToken:    "user-tok", // user path so linkVerifier (nil here) is skipped; channel already linked
		ChannelType:    2,
		ChannelID:      channelExtID,
		Messages: []ExtractMessage{{
			MessageID: "m_a", FromUID: "u_sender", FromUname: "孟豪", Timestamp: 1779344220,
			Content: "[文件]",
			Attachments: []ExtractMessageAttachment{{
				FileName: "report.pdf",
				FileURL:  "https://cdn.example/report.pdf",
			}},
		}},
	}
	entry, _, err := w.timelineSvc.CreateEntry(ctx, in)
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if entry == nil || entry.ID == "" {
		t.Fatal("entry should be persisted with non-empty id")
	}

	items, hasMore, err := w.outputsSvc.ListOutputs(ctx, matterID, "sp-it", []string{"u_sender"}, "", nil, 50)
	if err != nil {
		t.Fatalf("ListOutputs: %v", err)
	}
	if hasMore {
		t.Errorf("hasMore should be false for a single attachment")
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 output, got %d", len(items))
	}
	got := items[0]
	if got.FileURL != "https://cdn.example/report.pdf" {
		t.Errorf("file_url mismatch: %s", got.FileURL)
	}
	if got.FileName == nil || *got.FileName != "report.pdf" {
		t.Errorf("file_name mismatch: %+v", got.FileName)
	}
	if got.SenderUID != "u_sender" || got.SenderUname != "孟豪" {
		t.Errorf("sender fields mismatch: %+v", got)
	}
	if got.Description == nil || !strings.Contains(*got.Description, "孟豪") {
		t.Errorf("description should be the LLM-extracted content: %+v", got.Description)
	}
	if got.SourceChannelName == nil || *got.SourceChannelName != "测试群" {
		t.Errorf("source_channel_name JOIN broken: %+v", got.SourceChannelName)
	}
	want := time.Unix(1779344220, 0).UTC()
	if got.SentAt.Unix() != want.Unix() {
		t.Errorf("sent_at mismatch: got %s, want %s", got.SentAt, want)
	}
}

// TestOutputs_ServiceIT_DedupAcrossEntries forwards the same file via a
// second timeline entry and asserts the outputs view stays one-row-per-file.
func TestOutputs_ServiceIT_DedupAcrossEntries(t *testing.T) {
	w, cleanup := setupServiceIT(t)
	defer cleanup()

	ctx := context.Background()
	channelExtID := "ch-dedup-" + uuid.New().String()[:8]
	matterID := w.seedMatter(t, ctx, channelExtID, "dup-channel")

	url := "https://cdn.example/forwarded.pdf"
	w.llm.out = `{"content":"first upload","source_msg_ids":["m1"]}`
	in := TimelineInput{
		MatterID: matterID, SpaceID: "sp-it",
		ActorUID: "u1", ParticipantUID: "u1", CallerUIDs: []string{"u1"},
		CallerToken: "user-tok", ChannelType: 2, ChannelID: channelExtID,
		Messages: []ExtractMessage{{
			MessageID: "m1", FromUID: "u1", FromUname: "Alice", Timestamp: 1779000000,
			Attachments: []ExtractMessageAttachment{{FileName: "first.pdf", FileURL: url}},
		}},
	}
	if _, _, err := w.timelineSvc.CreateEntry(ctx, in); err != nil {
		t.Fatalf("first CreateEntry: %v", err)
	}

	// Same file_url forwarded by a different user in a later message.
	w.llm.out = `{"content":"forwarded","source_msg_ids":["m2"]}`
	in.Messages = []ExtractMessage{{
		MessageID: "m2", FromUID: "u2", FromUname: "Bob", Timestamp: 1779100000,
		Attachments: []ExtractMessageAttachment{{FileName: "fwd.pdf", FileURL: url}},
	}}
	if _, _, err := w.timelineSvc.CreateEntry(ctx, in); err != nil {
		t.Fatalf("second CreateEntry: %v", err)
	}

	items, _, err := w.outputsSvc.ListOutputs(ctx, matterID, "sp-it", []string{"u1"}, "", nil, 50)
	if err != nil {
		t.Fatalf("ListOutputs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected dedup to 1 row, got %d", len(items))
	}
	// Original sender wins — service skips the second insert because
	// FindByMatterAndFileURL hit the prior row.
	if items[0].SenderUID != "u1" || items[0].SenderUname != "Alice" {
		t.Errorf("first-upload sender should win, got %+v", items[0])
	}
}

// TestOutputs_ServiceIT_EmptyCitationsFailClosed is the regression guard for
// PR #35 reviewer feedback: when the LLM returns no source_msg_ids at all,
// the validator's all-messages fallback would previously cause every
// attachment in the batch to be persisted. After the fix, the attachment
// gate fails closed and no rows are written.
func TestOutputs_ServiceIT_EmptyCitationsFailClosed(t *testing.T) {
	w, cleanup := setupServiceIT(t)
	defer cleanup()

	ctx := context.Background()
	channelExtID := "ch-empty-cite-" + uuid.New().String()[:8]
	matterID := w.seedMatter(t, ctx, channelExtID, "ch")

	// LLM returns content but NO source_msg_ids. Timeline entry still
	// writes (the entry-level fallback kicks in), but attachments must NOT
	// leak.
	w.llm.out = `{"content":"missing citations"}`
	in := TimelineInput{
		MatterID: matterID, SpaceID: "sp-it",
		ActorUID: "u1", ParticipantUID: "u1", CallerUIDs: []string{"u1"},
		CallerToken: "user-tok", ChannelType: 2, ChannelID: channelExtID,
		Messages: []ExtractMessage{
			{
				MessageID: "m1", FromUID: "u1", Timestamp: 1779000000,
				Attachments: []ExtractMessageAttachment{{FileURL: "https://x/a.pdf"}},
			},
			{
				MessageID: "m2", FromUID: "u1", Timestamp: 1779000010,
				Attachments: []ExtractMessageAttachment{{FileURL: "https://x/b.pdf"}},
			},
		},
	}
	if _, _, err := w.timelineSvc.CreateEntry(ctx, in); err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}

	items, _, err := w.outputsSvc.ListOutputs(ctx, matterID, "sp-it", []string{"u1"}, "", nil, 50)
	if err != nil {
		t.Fatalf("ListOutputs: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("fail-closed: expected 0 attachments when LLM emitted no source_msg_ids, got %d", len(items))
	}
}

// TestOutputs_ServiceIT_AllInvalidCitationsFailClosed covers the related
// failure mode: LLM emits source_msg_ids that all fail input-set filtering.
// Same privacy guarantee applies.
func TestOutputs_ServiceIT_AllInvalidCitationsFailClosed(t *testing.T) {
	w, cleanup := setupServiceIT(t)
	defer cleanup()

	ctx := context.Background()
	channelExtID := "ch-bad-cite-" + uuid.New().String()[:8]
	matterID := w.seedMatter(t, ctx, channelExtID, "ch")

	// Every cited id is fabricated — filter drops them all, validator falls
	// back to all input msgs for the timeline entry, but attachments must
	// still fail closed.
	w.llm.out = `{"content":"hallucinated citations","source_msg_ids":["does-not-exist-1","does-not-exist-2"]}`
	in := TimelineInput{
		MatterID: matterID, SpaceID: "sp-it",
		ActorUID: "u1", ParticipantUID: "u1", CallerUIDs: []string{"u1"},
		CallerToken: "user-tok", ChannelType: 2, ChannelID: channelExtID,
		Messages: []ExtractMessage{{
			MessageID: "m1", FromUID: "u1", Timestamp: 1779000000,
			Attachments: []ExtractMessageAttachment{{FileURL: "https://x/secret.pdf"}},
		}},
	}
	if _, _, err := w.timelineSvc.CreateEntry(ctx, in); err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}

	items, _, err := w.outputsSvc.ListOutputs(ctx, matterID, "sp-it", []string{"u1"}, "", nil, 50)
	if err != nil {
		t.Fatalf("ListOutputs: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("fail-closed: expected 0 attachments under all-invalid citations, got %d", len(items))
	}
}

// TestOutputs_ServiceIT_UncitedMessagesDoNotLeakAttachments asserts the
// privacy property: a file in a message the LLM did NOT cite must not be
// stored on the matter. Otherwise an unrelated upload in the same chat
// window would surface in the matter's outputs view.
func TestOutputs_ServiceIT_UncitedMessagesDoNotLeakAttachments(t *testing.T) {
	w, cleanup := setupServiceIT(t)
	defer cleanup()

	ctx := context.Background()
	channelExtID := "ch-cite-" + uuid.New().String()[:8]
	matterID := w.seedMatter(t, ctx, channelExtID, "ch")

	// LLM cites only m_cited. m_other is just context — its attachment must
	// not be persisted.
	w.llm.out = `{"content":"only cited","source_msg_ids":["m_cited"]}`
	in := TimelineInput{
		MatterID: matterID, SpaceID: "sp-it",
		ActorUID: "u1", ParticipantUID: "u1", CallerUIDs: []string{"u1"},
		CallerToken: "user-tok", ChannelType: 2, ChannelID: channelExtID,
		Messages: []ExtractMessage{
			{
				MessageID: "m_cited", FromUID: "u1", Timestamp: 1779000000,
				Attachments: []ExtractMessageAttachment{{FileURL: "https://x/keep.pdf", FileName: "keep.pdf"}},
			},
			{
				MessageID: "m_other", FromUID: "u1", Timestamp: 1779000010,
				Attachments: []ExtractMessageAttachment{{FileURL: "https://x/leak.pdf", FileName: "leak.pdf"}},
			},
		},
	}
	if _, _, err := w.timelineSvc.CreateEntry(ctx, in); err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}

	items, _, err := w.outputsSvc.ListOutputs(ctx, matterID, "sp-it", []string{"u1"}, "", nil, 50)
	if err != nil {
		t.Fatalf("ListOutputs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected only the cited file to be persisted, got %d", len(items))
	}
	if items[0].FileURL != "https://x/keep.pdf" {
		t.Errorf("wrong file persisted: %+v", items[0].FileURL)
	}
}
