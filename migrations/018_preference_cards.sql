-- 018_preference_cards.sql
-- AI-era Zettelkasten: atomic behavior rules distilled from Matter feedback.
-- Each card = one reusable gotcha the agent should not repeat.

-- +migrate Up
CREATE TABLE preference_cards (
  id CHAR(36) NOT NULL PRIMARY KEY,
  space_id VARCHAR(64) NOT NULL,
  matter_id CHAR(36) NULL,
  project_id CHAR(36) NULL,
  agent_uid VARCHAR(64) NULL,
  creator_id VARCHAR(64) NOT NULL,
  status ENUM('draft','authorized','hit','miss','discarded') NOT NULL DEFAULT 'draft',
  scope ENUM('matter','project','bot','space','global') NOT NULL DEFAULT 'project',
  content TEXT NOT NULL COMMENT 'imperative reusable rule',
  evidence TEXT NULL COMMENT 'human signal citation',
  avoid TEXT NULL COMMENT 'when this rule should NOT apply',
  keywords JSON NULL COMMENT 'retrieval keywords',
  links JSON NULL COMMENT '[{"target_id":"...","reason":"..."}]',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  INDEX idx_space_status (space_id, status),
  INDEX idx_matter (matter_id),
  INDEX idx_agent (agent_uid),
  INDEX idx_project (project_id),
  FULLTEXT idx_content (content, evidence, avoid)
);

-- +migrate Down
DROP TABLE preference_cards;
