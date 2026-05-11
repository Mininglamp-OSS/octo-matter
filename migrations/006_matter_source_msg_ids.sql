-- 006_matter_source_msg_ids.sql

-- +migrate Up
ALTER TABLE matters
  ADD COLUMN source_msg_ids JSON NULL AFTER source_name;

-- +migrate Down
ALTER TABLE matters
  DROP COLUMN source_msg_ids;
