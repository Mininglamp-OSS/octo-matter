-- 007_timeline_attachment_outputs.sql

-- +migrate Up
ALTER TABLE matter_timeline_attachments
  ADD COLUMN matter_id    CHAR(36)     NOT NULL DEFAULT '' AFTER entry_id,
  ADD COLUMN description  TEXT         NULL     AFTER mime_type,
  ADD COLUMN sender_uid   VARCHAR(64)  NOT NULL DEFAULT '' AFTER description,
  ADD COLUMN sender_uname VARCHAR(128) NOT NULL DEFAULT '' AFTER sender_uid,
  ADD COLUMN sent_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) AFTER sender_uname;

-- Backfill matter_id + sent_at from the owning timeline entry so legacy
-- direct-path attachments are visible in the outputs view.
UPDATE matter_timeline_attachments a
  JOIN matter_timelines t ON t.id = a.entry_id
   SET a.matter_id = t.matter_id,
       a.sent_at   = t.created_at
 WHERE a.matter_id = '';

-- Outputs list is keyed by (matter_id, sent_at DESC, id) — index supports
-- both the cursor walk and the per-file_url GROUP BY MIN(id) dedup query.
-- file_url is a utf8mb4 varchar(1024) → up to 4096 bytes, which busts the
-- 3072-byte InnoDB key limit when prefixed with matter_id. A 190-char prefix
-- (=760 bytes) is long enough to narrow candidates for dedup; the GROUP BY
-- still groups on the full column so correctness is unaffected.
ALTER TABLE matter_timeline_attachments
  ADD INDEX idx_attachments_matter_sent (matter_id, sent_at, id),
  ADD INDEX idx_attachments_matter_file (matter_id, file_url(190));

-- +migrate Down
ALTER TABLE matter_timeline_attachments
  DROP INDEX idx_attachments_matter_file,
  DROP INDEX idx_attachments_matter_sent,
  DROP COLUMN sent_at,
  DROP COLUMN sender_uname,
  DROP COLUMN sender_uid,
  DROP COLUMN description,
  DROP COLUMN matter_id;
