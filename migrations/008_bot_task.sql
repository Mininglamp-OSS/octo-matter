-- 008_bot_task.sql
-- PR-B: bot_task moves from octo-fleet's schema to octo-matter so that
-- matter can write tasks in the same transaction that creates the
-- triggering timeline entry (no risk of "comment saved but dispatch
-- lost" if matter crashes mid-handler).
--
-- Daemon pulls tasks from matter (GET /v1/internal/bot-tasks?bot_uid=X)
-- using its daemon-scope JWT, and acks via POST .../:id/ack. Fleet no
-- longer carries bot_task.

-- +migrate Up
CREATE TABLE IF NOT EXISTS matter_bot_task (
  id                CHAR(36)     NOT NULL,
  matter_id         CHAR(36)     NOT NULL,
  space_id          VARCHAR(64)  NOT NULL,
  bot_uid           VARCHAR(64)  NOT NULL,
  trigger_kind      VARCHAR(32)  NOT NULL DEFAULT 'mention',
  trigger_entry_id  CHAR(36)     NULL,
  prompt            MEDIUMTEXT   NOT NULL,
  matter_title      VARCHAR(255) NOT NULL DEFAULT '',
  status            VARCHAR(16)  NOT NULL DEFAULT 'queued',
  claim_token       VARCHAR(64)  NULL,
  claimed_by        VARCHAR(64)  NULL,
  claimed_at        DATETIME(3)  NULL,
  lease_until       DATETIME(3)  NULL,
  attempt           INT          NOT NULL DEFAULT 0,
  max_attempts      INT          NOT NULL DEFAULT 3,
  error_msg         TEXT         NULL,
  result_summary    TEXT         NULL,
  elapsed_ms        BIGINT       NULL,
  created_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_bot_status (bot_uid, status, created_at),
  KEY idx_matter (matter_id),
  KEY idx_claim_lease (status, lease_until),
  -- Dedup per (matter, bot, trigger): a single comment can't enqueue
  -- the same bot twice. NULL trigger_entry_id permits multiple
  -- programmatic dispatches (UNIQUE in MySQL allows multi-NULL).
  UNIQUE KEY uk_trigger (matter_id, bot_uid, trigger_entry_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +migrate Down
DROP TABLE IF EXISTS matter_bot_task;
