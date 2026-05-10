-- 005_unify_timeline.sql

-- +migrate Up
ALTER TABLE matter_comments DROP FOREIGN KEY fk_comments_channel;
ALTER TABLE matter_comment_attachments DROP FOREIGN KEY fk_matter_comment_attachments_comment;

ALTER TABLE matter_comment_attachments
  CHANGE comment_id entry_id CHAR(36) NOT NULL;

RENAME TABLE
  matter_comments TO matter_timelines,
  matter_comment_attachments TO matter_timeline_attachments;

ALTER TABLE matter_timelines
  DROP INDEX idx_comments_matter_channel,
  ADD INDEX idx_timelines_matter_channel (matter_id, source_channel_id, created_at),
  ADD CONSTRAINT fk_timelines_channel
    FOREIGN KEY (channel_id) REFERENCES matter_channels(id) ON DELETE SET NULL;

ALTER TABLE matter_timeline_attachments
  ADD CONSTRAINT fk_timeline_attachments_entry
    FOREIGN KEY (entry_id) REFERENCES matter_timelines(id) ON DELETE CASCADE;

-- +migrate Down
ALTER TABLE matter_timeline_attachments DROP FOREIGN KEY fk_timeline_attachments_entry;
ALTER TABLE matter_timelines DROP FOREIGN KEY fk_timelines_channel;

ALTER TABLE matter_timelines
  DROP INDEX idx_timelines_matter_channel,
  ADD INDEX idx_comments_matter_channel (matter_id, source_channel_id, created_at);

RENAME TABLE
  matter_timelines TO matter_comments,
  matter_timeline_attachments TO matter_comment_attachments;

ALTER TABLE matter_comment_attachments
  CHANGE entry_id comment_id CHAR(36) NOT NULL;

ALTER TABLE matter_comments
  ADD CONSTRAINT fk_comments_channel
    FOREIGN KEY (channel_id) REFERENCES matter_channels(id) ON DELETE SET NULL;

ALTER TABLE matter_comment_attachments
  ADD CONSTRAINT fk_matter_comment_attachments_comment
    FOREIGN KEY (comment_id) REFERENCES matter_comments(id) ON DELETE CASCADE;
