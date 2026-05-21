//go:build integration

package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/google/uuid"
)

// wipeAllTables drops every table in the current DB with FK checks disabled,
// so renamed-from-legacy tables don't block the reset. Duplicated in the
// service-package IT — test packages can't share helpers across boundaries
// without exporting them, and exporting would leak into production code.
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

// Integration tests for the matter outputs view + the LLM-path attachment
// persistence. Skipped unless MATTER_OUTPUTS_IT_DSN is set; gate with the
// "integration" build tag so a bare `go test ./...` stays hermetic.
//
// Run with:
//
//	MATTER_OUTPUTS_IT_DSN='root:demo@tcp(127.0.0.1:3306)/octo_matter_outputs_it?charset=utf8mb4&parseTime=true&multiStatements=true' \
//	  go test -tags=integration ./internal/repository/... -run Outputs -v

func dsn(t *testing.T) string {
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

func setupDB(t *testing.T) (cleanup func()) {
	t.Helper()
	d := dsn(t)
	conn, _, err := NewSession(d)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := wipeAllTables(conn.DB); err != nil {
		conn.Close()
		t.Fatalf("wipe schema: %v", err)
	}
	if _, err := RunMigrations(conn); err != nil {
		conn.Close()
		t.Fatalf("migrate: %v", err)
	}
	return func() { conn.Close() }
}

// seedMatterAndChannel inserts the minimum rows we need before adding
// timeline entries and attachments. Returns matterID, channelLinkID.
func seedMatterAndChannel(t *testing.T, ctx context.Context, dsnStr string) (matterID, channelLinkID string) {
	t.Helper()
	conn, _, err := NewSession(dsnStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	matterID = uuid.New().String()
	channelLinkID = uuid.New().String()
	channelExtID := "ch-test-" + uuid.New().String()[:8]

	if _, err := conn.DB.ExecContext(ctx,
		`INSERT INTO matters (id, seq_no, space_id, title, creator_id, status, created_at, updated_at)
		 VALUES (?, 1, 'sp-it', 'IT matter', 'creator', 'open', NOW(3), NOW(3))`,
		matterID); err != nil {
		t.Fatalf("insert matter: %v", err)
	}
	chName := "测试群"
	if _, err := conn.DB.ExecContext(ctx,
		`INSERT INTO matter_channels (id, matter_id, channel_id, channel_type, channel_name, linked_by, created_at)
		 VALUES (?, ?, ?, 2, ?, 'creator', NOW(3))`,
		channelLinkID, matterID, channelExtID, chName); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return matterID, channelLinkID
}

// insertTimelineEntry writes a matter_timelines row owned by the channelLink
// and returns its id.
func insertTimelineEntry(t *testing.T, ctx context.Context, dsnStr, matterID, channelLinkID string) string {
	t.Helper()
	conn, _, err := NewSession(dsnStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	id := uuid.New().String()
	if _, err := conn.DB.ExecContext(ctx,
		`INSERT INTO matter_timelines (id, matter_id, user_id, content, channel_id, channel_type, source_channel_id, created_at)
		 VALUES (?, ?, 'u_sender', 'entry', ?, 2, 'ch-ext', NOW(3))`,
		id, matterID, channelLinkID); err != nil {
		t.Fatalf("insert timeline: %v", err)
	}
	return id
}

func TestOutputs_Integration_PersistAndList(t *testing.T) {
	d := dsn(t)
	cleanup := setupDB(t)
	defer cleanup()

	ctx := context.Background()
	matterID, chLink := seedMatterAndChannel(t, ctx, d)
	entryID := insertTimelineEntry(t, ctx, d, matterID, chLink)

	conn, sess, err := NewSession(d)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	repo := NewTimelineAttachmentRepo(sess)

	pdfName := "report.pdf"
	size := int64(1234567)
	desc := "孟豪上传了发布会文稿《report.pdf》"
	sent := time.Unix(1779344220, 0).UTC()

	if err := repo.CreateMany(ctx, []*model.TimelineAttachment{{
		EntryID:     entryID,
		MatterID:    matterID,
		FileURL:     "https://cdn.example/report.pdf",
		FileName:    &pdfName,
		FileSize:    &size,
		Description: &desc,
		SenderUID:   "u_sender",
		SenderUname: "孟豪",
		SentAt:      sent,
	}}); err != nil {
		t.Fatalf("CreateMany: %v", err)
	}

	items, hasMore, err := repo.ListOutputs(ctx, OutputsFilter{MatterID: matterID, Limit: 50})
	if err != nil {
		t.Fatalf("ListOutputs: %v", err)
	}
	if hasMore {
		t.Errorf("hasMore should be false for a single row")
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 output, got %d", len(items))
	}
	got := items[0]
	if got.MatterID != matterID || got.EntryID != entryID {
		t.Errorf("scope fields mismatch: %+v", got)
	}
	if got.FileURL != "https://cdn.example/report.pdf" {
		t.Errorf("file_url mismatch: %s", got.FileURL)
	}
	if got.FileName == nil || *got.FileName != "report.pdf" {
		t.Errorf("file_name mismatch: %+v", got.FileName)
	}
	if got.FileSize == nil || *got.FileSize != 1234567 {
		t.Errorf("file_size mismatch: %+v", got.FileSize)
	}
	if got.Description == nil || !strings.Contains(*got.Description, "孟豪") {
		t.Errorf("description should round-trip utf8mb4: %+v", got.Description)
	}
	if got.SenderUID != "u_sender" || got.SenderUname != "孟豪" {
		t.Errorf("sender fields mismatch: %+v", got)
	}
	if got.SourceChannelName == nil || *got.SourceChannelName != "测试群" {
		t.Errorf("source_channel_name should come from JOIN matter_channels: %+v", got.SourceChannelName)
	}
	if !got.SentAt.Equal(sent) {
		// MySQL stores DATETIME(3) — second precision is enough here, but
		// double-check the timezone round-trip.
		if got.SentAt.Unix() != sent.Unix() {
			t.Errorf("sent_at mismatch: got %s, want %s", got.SentAt, sent)
		}
	}
}

// TestOutputs_Integration_DedupPicksEarliestSentAt is the regression guard
// for the original-upload-wins property. Insert order is inverse to sent_at
// order, and UUIDs are random, so a MIN(id)-based dedup would pick the
// wrong row roughly 50% of the time. With the (sent_at, id) anti-join
// dedup, the earliest sent_at always wins.
func TestOutputs_Integration_DedupPicksEarliestSentAt(t *testing.T) {
	d := dsn(t)
	cleanup := setupDB(t)
	defer cleanup()

	ctx := context.Background()
	matterID, chLink := seedMatterAndChannel(t, ctx, d)

	conn, sess, err := NewSession(d)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	repo := NewTimelineAttachmentRepo(sess)

	url := "https://cdn.example/canonical.pdf"

	// Run the scenario multiple times. UUIDs are random — a single pass
	// could pass by luck under a broken MIN(id) dedup. 5 iterations make
	// the false-pass probability < 4%.
	for i := 0; i < 5; i++ {
		// Each iteration is its own pair of timeline entries.
		laterEntry := insertTimelineEntry(t, ctx, d, matterID, chLink)
		earlierEntry := insertTimelineEntry(t, ctx, d, matterID, chLink)

		// IMPORTANT: insert the LATER-sent_at row FIRST. Under a broken
		// MIN(id) dedup, the winner is whichever UUID sorts smaller —
		// random per iteration. Under the correct (sent_at, id) dedup,
		// the row with the earlier sent_at always wins.
		if err := repo.CreateMany(ctx, []*model.TimelineAttachment{{
			EntryID:     laterEntry,
			MatterID:    matterID,
			FileURL:     url,
			SenderUID:   "u_forwarder",
			SenderUname: "Forwarder",
			SentAt:      time.Unix(2000000000+int64(i), 0).UTC(),
		}}); err != nil {
			t.Fatalf("insert later: %v", err)
		}
		if err := repo.CreateMany(ctx, []*model.TimelineAttachment{{
			EntryID:     earlierEntry,
			MatterID:    matterID,
			FileURL:     url,
			SenderUID:   "u_original",
			SenderUname: "Original",
			SentAt:      time.Unix(1000000000+int64(i), 0).UTC(),
		}}); err != nil {
			t.Fatalf("insert earlier: %v", err)
		}

		items, _, err := repo.ListOutputs(ctx, OutputsFilter{MatterID: matterID, Limit: 50})
		if err != nil {
			t.Fatalf("ListOutputs iter %d: %v", i, err)
		}
		if len(items) != 1 {
			t.Fatalf("iter %d: expected dedup to 1 row, got %d", i, len(items))
		}
		if items[0].SenderUID != "u_original" {
			t.Fatalf("iter %d: earliest sent_at must win the dedup, got sender=%s", i, items[0].SenderUID)
		}

		// Wipe the attachments table between iterations so each round is
		// isolated. Keeping the same matter/channel avoids re-seeding.
		if _, err := conn.DB.ExecContext(ctx, "DELETE FROM matter_timeline_attachments WHERE matter_id = ?", matterID); err != nil {
			t.Fatalf("cleanup iter %d: %v", i, err)
		}
	}
}

func TestOutputs_Integration_DedupsByFileURL(t *testing.T) {
	d := dsn(t)
	cleanup := setupDB(t)
	defer cleanup()

	ctx := context.Background()
	matterID, chLink := seedMatterAndChannel(t, ctx, d)
	entry1 := insertTimelineEntry(t, ctx, d, matterID, chLink)
	entry2 := insertTimelineEntry(t, ctx, d, matterID, chLink)

	conn, sess, err := NewSession(d)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	repo := NewTimelineAttachmentRepo(sess)

	url := "https://cdn.example/dup.pdf"
	firstName := "first.pdf"
	secondName := "second.pdf"

	// First upload — earlier sent_at, this row should win the dedup.
	if err := repo.CreateMany(ctx, []*model.TimelineAttachment{{
		EntryID:     entry1,
		MatterID:    matterID,
		FileURL:     url,
		FileName:    &firstName,
		SenderUID:   "u_orig",
		SenderUname: "Original",
		SentAt:      time.Unix(1779000000, 0).UTC(),
	}}); err != nil {
		t.Fatalf("CreateMany 1: %v", err)
	}
	// Forwarded — later sent_at, same file_url, different sender.
	if err := repo.CreateMany(ctx, []*model.TimelineAttachment{{
		EntryID:     entry2,
		MatterID:    matterID,
		FileURL:     url,
		FileName:    &secondName,
		SenderUID:   "u_fwd",
		SenderUname: "Forwarder",
		SentAt:      time.Unix(1779100000, 0).UTC(),
	}}); err != nil {
		t.Fatalf("CreateMany 2: %v", err)
	}

	items, _, err := repo.ListOutputs(ctx, OutputsFilter{MatterID: matterID, Limit: 50})
	if err != nil {
		t.Fatalf("ListOutputs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected dedup to 1 row, got %d", len(items))
	}
	if items[0].EntryID != entry1 {
		t.Errorf("earliest row (MIN(id)) should win the dedup, got entry %s", items[0].EntryID)
	}
}

func TestOutputs_Integration_QFilterAndPagination(t *testing.T) {
	d := dsn(t)
	cleanup := setupDB(t)
	defer cleanup()

	ctx := context.Background()
	matterID, chLink := seedMatterAndChannel(t, ctx, d)
	entry := insertTimelineEntry(t, ctx, d, matterID, chLink)

	conn, sess, err := NewSession(d)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	repo := NewTimelineAttachmentRepo(sess)

	// Three rows, distinct file_urls, sent_at apart so cursor pagination is
	// deterministic. Names chosen so a q filter can isolate "report".
	rows := []*model.TimelineAttachment{
		newAtt(entry, matterID, "https://x/report-q1.pdf", "report-q1.pdf", "Alice", 1779000000),
		newAtt(entry, matterID, "https://x/notes.md", "notes.md", "Bob", 1779100000),
		newAtt(entry, matterID, "https://x/report-q2.pdf", "report-q2.pdf", "Carol", 1779200000),
	}
	for _, r := range rows {
		if err := repo.CreateMany(ctx, []*model.TimelineAttachment{r}); err != nil {
			t.Fatalf("CreateMany: %v", err)
		}
	}

	// q filter: only the two "report" files.
	items, _, err := repo.ListOutputs(ctx, OutputsFilter{MatterID: matterID, Q: "report", Limit: 50})
	if err != nil {
		t.Fatalf("ListOutputs q=report: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("q=report expected 2 rows, got %d", len(items))
	}
	for _, it := range items {
		if it.FileName == nil || !strings.Contains(*it.FileName, "report") {
			t.Errorf("q filter leaked non-matching row: %+v", it.FileName)
		}
	}

	// Pagination: limit=1, walk three pages.
	pageOne, hasMore, err := repo.ListOutputs(ctx, OutputsFilter{MatterID: matterID, Limit: 1})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if !hasMore {
		t.Fatalf("page 1 should report has_more=true")
	}
	if len(pageOne) != 1 {
		t.Fatalf("page 1 expected 1 row, got %d", len(pageOne))
	}
	// Newest first — report-q2 (sent_at 1779200000).
	if pageOne[0].FileName == nil || *pageOne[0].FileName != "report-q2.pdf" {
		t.Errorf("page 1 should be newest, got %+v", pageOne[0].FileName)
	}
	cur := EncodeCursor(Cursor{CreatedAt: pageOne[0].SentAt, ID: pageOne[0].ID})
	pageTwo, hasMore2, err := repo.ListOutputs(ctx, OutputsFilter{MatterID: matterID, Limit: 1, Cursor: &cur})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if !hasMore2 {
		t.Fatalf("page 2 should still report has_more=true (one row remaining)")
	}
	if len(pageTwo) != 1 || *pageTwo[0].FileName != "notes.md" {
		t.Fatalf("page 2 mismatch: %+v", pageTwo)
	}
	cur = EncodeCursor(Cursor{CreatedAt: pageTwo[0].SentAt, ID: pageTwo[0].ID})
	pageThree, hasMore3, err := repo.ListOutputs(ctx, OutputsFilter{MatterID: matterID, Limit: 1, Cursor: &cur})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if hasMore3 {
		t.Errorf("final page should not report has_more")
	}
	if len(pageThree) != 1 || *pageThree[0].FileName != "report-q1.pdf" {
		t.Fatalf("page 3 mismatch: %+v", pageThree)
	}
}

func newAtt(entryID, matterID, url, name, sender string, ts int64) *model.TimelineAttachment {
	n := name
	return &model.TimelineAttachment{
		EntryID:     entryID,
		MatterID:    matterID,
		FileURL:     url,
		FileName:    &n,
		SenderUID:   "u_" + sender,
		SenderUname: sender,
		SentAt:      time.Unix(ts, 0).UTC(),
	}
}
