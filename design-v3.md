# Matter Intelligent Digest — Design Document (v3 Final)

> Supersedes v2-final. Incorporates architecture review corrections from 齐静春 + code-level review from 李飞飞.

## 1. Overview

### Background

The matter service introduces two AI-powered APIs:
1. **Create Matter** — Extract a new matter (title, description, deadline) from user-selected chat messages
2. **Add Matter Timeline** — Extract progress updates for an existing matter from chat messages

Both are synchronous, single LLM call, no worker queue.

### Design Principles

- No worker queue — synchronous API, single LLM call per request
- No auto-sync — user-initiated only
- Minimal new entities — reuse existing tables where possible
- Messages provided by frontend — matter service does not connect to IM database

---

## 2. Schema Upgrade (Migration 004)

### 2.1 Current State (Post Migration 003)

| Table | Purpose |
|-------|---------|
| matters | Core task entity (id=CHAR(36) UUID) |
| matter_assignees | Responsible users |
| matter_participants | Observers |
| matter_channels | Linked chat channels |
| matter_comments | Comments (text) |
| matter_comment_attachments | Comment file attachments |

### 2.2 Changes

#### matters — Add space-scoped sequence number

```sql
ALTER TABLE matters
  ADD COLUMN seq_no INT UNSIGNED NULL AFTER id,
  ADD UNIQUE KEY uk_space_seq (space_id, seq_no);
```

Display format: `M-{seq_no}` (e.g., M-2451)

**Backfill strategy** (MySQL 8.0+ safe — uses window function, not user variables):

```sql
UPDATE matters m
JOIN (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at) AS rn
  FROM matters
) ranked ON m.id = ranked.id
SET m.seq_no = ranked.rn;
```

Then make NOT NULL:
```sql
ALTER TABLE matters MODIFY seq_no INT UNSIGNED NOT NULL;
```

**New record seq_no generation** (application-level with retry-on-duplicate):

```go
// In MatterRepo.Create, within the CreateMatterWithAssignees transaction:
func (r *MatterRepo) nextSeqNo(ctx context.Context, spaceID string) (int, error) {
    var next int
    err := r.runner.SelectBySql(
        "SELECT COALESCE(MAX(seq_no), 0) + 1 FROM matters WHERE space_id = ? FOR UPDATE",
        spaceID,
    ).LoadOneContext(ctx, &next)
    return next, err
}

// Caller wraps with retry on duplicate key:
for retries := 0; retries < 3; retries++ {
    seq, err := r.nextSeqNo(ctx, matter.SpaceID)
    if err != nil { return err }
    matter.SeqNo = seq
    err = r.insertMatter(ctx, matter)
    if err == nil { break }
    if !isDuplicateKeyErr(err) { return err }
    // retry: concurrent insert raced us
}
```

This is isolation-level-agnostic: works correctly under both REPEATABLE READ and READ COMMITTED. The UNIQUE KEY `uk_space_seq` is the ultimate safety net.

#### matter_comments — Add channel association + source data

```sql
ALTER TABLE matter_comments
  ADD COLUMN channel_id CHAR(36) NULL,
  ADD COLUMN channel_type TINYINT UNSIGNED NULL,
  ADD COLUMN source_channel_id VARCHAR(255) NULL,
  ADD COLUMN source_msgs JSON NULL,
  ADD COLUMN related_uids JSON NULL,
  ADD INDEX idx_comments_matter_channel (matter_id, source_channel_id, created_at),
  ADD CONSTRAINT fk_comments_channel
    FOREIGN KEY (channel_id) REFERENCES matter_channels(id) ON DELETE SET NULL;
```

Semantics:
- `channel_id = NULL` + `source_channel_id = NULL` → regular user comment
- `source_channel_id IS NOT NULL` → digest extracted from that channel
- `channel_id` → FK to matter_channels (becomes NULL if channel unlinked)
- `source_channel_id` → raw channel ID string (survives FK SET NULL, used for display/query)
- `source_msgs` → JSON array of referenced message IDs
- `related_uids` → JSON array of involved user UIDs

**Identifying digests in queries** (important: do NOT use `channel_id IS NOT NULL`):
```sql
-- Correct: survives ON DELETE SET NULL
WHERE source_channel_id IS NOT NULL

-- Incorrect: becomes NULL when channel is unlinked
WHERE channel_id IS NOT NULL
```

**Index rationale**: Compound index `(matter_id, source_channel_id, created_at)` covers:
1. List all digests for a matter (WHERE matter_id = ? AND source_channel_id IS NOT NULL)
2. List digests for a specific channel within a matter (WHERE matter_id = ? AND source_channel_id = ?)
3. Ordered by time without filesort

#### Clean up legacy index

```sql
-- idx_comments_todo is a legacy name from pre-rename era, superseded by compound index
ALTER TABLE matter_comments DROP INDEX idx_comments_todo;
```

#### matter_activities — New table (change log)

```sql
CREATE TABLE IF NOT EXISTS matter_activities (
    id          CHAR(36)    NOT NULL,
    matter_id   CHAR(36)    NOT NULL,
    actor_id    VARCHAR(64) NOT NULL,
    action      VARCHAR(50) NOT NULL,
    detail      JSON        NULL,
    created_at  DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_activity_matter (matter_id, created_at DESC),
    CONSTRAINT fk_activity_matter FOREIGN KEY (matter_id)
        REFERENCES matters(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### 2.3 Entity Reuse Decisions

| UI Concept | Implementation |
|------------|---------------|
| Per-channel digest timeline | matter_comments WHERE source_channel_id IS NOT NULL, grouped by source_channel_id |
| Output files tab | Aggregate matter_comment_attachments by matter_id |
| Change log tab | matter_activities (only new entity) |

---

## 3. API Design

### 3.1 Create Matter (AI-powered)

Creates a new matter by extracting structured information from selected chat messages.

#### Endpoint

```
POST /api/v1/matters/extract
```

#### Request Body

```json
{
  "channel_type": 2,
  "channel_id": "group_no_xxx",
  "creator_uid": "uid_xxx",
  "msgs": [
    {
      "message_id": "msg_001",
      "from_uid": "uid_xxx",
      "from_uname": "王宜林",
      "timestamp": 1715234400,
      "content": "5/15 之前要把 Octo 策略 PPT 搞定给董事会",
      "attachments": [
        {
          "file_name": "outline.md",
          "file_url": "https://..."
        }
      ]
    }
  ]
}
```

**Auth note on `creator_uid`**: This API is called by the frontend/Bot proxy, not directly by end users. When called via Bot token auth, `creator_uid` specifies the actual human creator. The Bot must have authorization to act on behalf of this user (verified via Space membership). When called via User token auth, `creator_uid` must equal the authenticated user's UID (server-side validation).

#### Response Body

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "seq_no": 2451,
  "title": "Octo 产品策略 PPT 打磨",
  "description": "5/15 给董事会做 30min PPT，讲清 Octo 跟 Linear/玛蒂卡的差异 + GTM 路径",
  "source_msgs": ["msg_001", "msg_003"],
  "deadline": 1747296000,
  "status": "open"
}
```

**ID convention**: All entity IDs (`id`, `entry_id`) are UUID strings (CHAR(36)). `seq_no` is the human-friendly display number (M-2451). API consumers should use `id` (UUID) for all subsequent API calls. `seq_no` is display-only.

#### Processing Flow

```
Request → Auth (Bot or User token) →
Validate creator_uid authorization →
LLM extract_matter Function Call →
Begin Transaction:
  Create matter record (with retry-on-duplicate for seq_no) →
  Auto-link source channel (matter_channels) →
  Add creator as assignee →
Commit →
Record activity (created) [best-effort, post-tx] →
Return
```

#### LLM Tool: `extract_matter`

```json
{
  "name": "extract_matter",
  "description": "从聊天记录中提取事项信息",
  "parameters": {
    "type": "object",
    "properties": {
      "title": {
        "type": "string",
        "description": "事项标题，简洁明确，不超过100字"
      },
      "description": {
        "type": "string",
        "description": "事项描述/目标"
      },
      "deadline": {
        "type": ["number", "null"],
        "description": "Unix timestamp，从消息中推断的截止时间，无法推断时为 null"
      },
      "source_msg_ids": {
        "type": "array",
        "items": { "type": "string" },
        "description": "与事项直接相关的消息 ID"
      },
      "assignee_uids": {
        "type": "array",
        "items": { "type": "string" },
        "description": "推断的负责人 UID（从消息上下文中识别）"
      }
    },
    "required": ["title", "description", "deadline", "source_msg_ids", "assignee_uids"]
  }
}
```

---

### 3.2 Add Matter Timeline (AI-powered digest)

Extracts progress information from selected messages and adds to the matter's channel timeline.

#### Endpoint

```
POST /api/v1/matters/:id/timeline
```

#### Request Body

```json
{
  "channel_type": 2,
  "channel_id": "group_no_xxx",
  "participant_uid": "uid_xxx",
  "matter_id": "550e8400-e29b-41d4-a716-446655440000",
  "msgs": [
    {
      "message_id": "msg_101",
      "from_uid": "uid_xxx",
      "from_uname": "吴明辉",
      "timestamp": 1715320800,
      "content": "Coze 接入路径必须作为第4章传播关键钩子",
      "attachments": []
    }
  ]
}
```

**Note on `matter_id` in body**: Redundant with path param `:id` but retained for forward compatibility. Server MUST validate `body.matter_id == path.id`; return 400 if mismatch.

**Auth note on `participant_uid`**: Same pattern as `creator_uid` — specifies the actual human actor. Bot auth must verify it can act on behalf of this user. For Timeline API, the participant_uid (or its owning user if bot) must have **write access** to the matter.

#### Response Body

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "seq_no": 2451,
  "entry_id": "660e8400-e29b-41d4-a716-446655440001",
  "timestamp": 1715320800,
  "related_uids": ["uid_wuminghui", "uid_wangyilin"],
  "content": "吴明辉明确 Coze 接入路径必须作为第4章传播关键钩子，是资本市场破圈点。",
  "channel_id": "group_no_xxx",
  "channel_type": 2,
  "source_msgs": ["msg_101", "msg_103"]
}
```

#### Processing Flow (with channel_id FK lookup)

```
Request → Auth (Bot or User token) →
Validate participant_uid authorization →
Validate body.matter_id == path :id (400 if mismatch) →
Load Matter + Access Check (participant_uid must have write access) →
Load Matter Context (title/desc/status/deadline/assignees) →
LLM extract_matter_progress Function Call →
Channel FK Resolution:
  1. Query matter_channels WHERE matter_id = :id AND channel_id = req.channel_id
  2. If not found → auto-link channel (INSERT matter_channels row)
  3. Obtain matter_channels.id (UUID) for FK →
Write matter_comments:
  channel_id = matter_channels.id (UUID, FK)
  source_channel_id = req.channel_id (raw string, display)
  channel_type = req.channel_type
  source_msgs = LLM output
  related_uids = LLM output
  content = LLM progress_summary →
Upsert participant as matter_participant →
Return
```

#### LLM Tool: `extract_matter_progress`

```json
{
  "name": "extract_matter_progress",
  "description": "从聊天记录中提取与目标事项相关的进展信息",
  "parameters": {
    "type": "object",
    "properties": {
      "content": {
        "type": "string",
        "description": "一段话概括与该事项相关的最新进展"
      },
      "related_uids": {
        "type": "array",
        "items": { "type": "string" },
        "description": "本次进展涉及的人员 UID"
      },
      "source_msg_ids": {
        "type": "array",
        "items": { "type": "string" },
        "description": "支撑此进展的消息 ID"
      },
      "status_suggestion": {
        "type": ["string", "null"],
        "enum": ["open", "done", null],
        "description": "建议状态变更，无建议时为 null"
      }
    },
    "required": ["content", "related_uids", "source_msg_ids", "status_suggestion"]
  }
}
```

---

## 4. Activity Tracking

### 4.1 Architecture Decision: Best-effort Post-mutation

Activity recording is **NOT** wrapped in the same transaction as the mutation. Rationale:
- Most mutation methods (UpdateMatter, AddAssignee, RemoveAssignee, LinkChannel, UnlinkChannel) are currently non-transactional single-statement operations. Wrapping them all in tx.Do() is a large refactor with regression risk.
- Activity loss on INSERT failure (extremely rare) does not affect business correctness.
- Consistent with existing notification pattern (worker.Submit post-mutation).
- If future requirements demand strict consistency, wrapping in tx is a non-breaking change.

**Pattern:**
```go
// In handler or service layer, AFTER mutation succeeds:
if err := activityRepo.Record(ctx, matterID, actorID, "title_changed", detail); err != nil {
    log.Printf("[WARN] activity record failed: %v", err) // best-effort
}
```

**Exception**: `SetStatus` already runs in tx.Do(). Activity for status_changed CAN be written inside the same tx if ActivityRepo is available on TxRepos. Optional optimization, not required for v1.

### 4.2 Tracked Actions

| action | Trigger | detail example |
|--------|---------|----------------|
| created | Matter creation | `{}` |
| title_changed | Title update | `{"from":"old","to":"new"}` |
| description_changed | Description update | `{"summary":"..."}` |
| status_changed | Status transition | `{"from":"open","to":"done"}` |
| deadline_changed | Deadline update | `{"from":1715296000,"to":1747296000}` |
| assignee_added | Add assignee | `{"user_id":"x","user_name":"张三"}` |
| assignee_removed | Remove assignee | `{"user_id":"x","user_name":"张三"}` |
| channel_linked | Link channel | `{"channel_id":"x","channel_name":"#群名"}` |
| channel_unlinked | Unlink channel | `{"channel_id":"x"}` |

### 4.3 NOT Tracked

- Digest/comment additions (queryable directly via matter_comments)
- File uploads (tracked via comment_attachments)

---

## 5. Permission Model

### Write Access (can mutate matter)

- **Creator** — full control (edit, delete, manage assignees/channels, create digest)
- **Assignees** — edit title/description/status/deadline, link channels, create digest

### Read Access (view only)

- **Participants** — can view, comment, but not edit matter fields
- **Linked channel members** — can view (via channel membership)

### API-specific Auth

| Endpoint | Auth path | Access rule |
|----------|-----------|-------------|
| POST /extract | Bot or User token | Any authenticated user in the Space (they become creator) |
| POST /:id/timeline | Bot or User token | participant_uid (or its owner) must have write access to matter |

**Bot → Owner resolution for Timeline API**: When bot token is used, the bot's owner_uid must be resolved and checked for write access. This requires the auth middleware to expand `related_uids` to include the bot's owner (same as existing owned_bots expansion in user auth path, but inverted for bot auth).

---

## 6. LLM Integration

### Code Structure

```
internal/
  llm/
    client.go             -- Thin OpenAI-compatible HTTP client (function calling)
  service/
    digest_svc.go         -- Timeline digest: prompt assembly + response parsing
    extract_svc.go        -- Create matter: prompt assembly + response parsing
  handler/
    digest_handler.go     -- POST /matters/:id/timeline
    extract_handler.go    -- POST /matters/extract
```

### System Prompt Context

**Create Matter:**
- Channel name (if resolvable)
- All message senders and their UIDs
- Current date/time (for deadline inference)

**Add Timeline:**
- Matter title + description + current status + deadline
- Assignee names + UIDs
- Channel name
- Last 3 existing timeline entries (for context continuity)

### Configuration

```env
LLM_API_URL=https://api.example.com/v1
LLM_API_KEY=<key>
LLM_MODEL=claude-sonnet-4-6
LLM_TIMEOUT=30
```

Config struct additions in `internal/config/config.go`:
```go
LLM_API_URL string
LLM_API_KEY string
LLM_MODEL   string
LLM_TIMEOUT int // seconds
```

---

## 7. Code Changes Required

### 7.1 Model Layer

**model/matter.go** — Add SeqNo field:
```go
SeqNo int `db:"seq_no" json:"seq_no"`
```

**model/comment.go** — Add digest fields:
```go
ChannelID       *string `db:"channel_id" json:"channel_id,omitempty"`
ChannelType     *uint8  `db:"channel_type" json:"channel_type,omitempty"`
SourceChannelID *string `db:"source_channel_id" json:"source_channel_id,omitempty"`
SourceMsgs      *string `db:"source_msgs" json:"source_msgs,omitempty"`     // JSON string
RelatedUIDs     *string `db:"related_uids" json:"related_uids,omitempty"`   // JSON string
```

Since `ListByMatter` uses `SELECT *`, existing queries will automatically pick up new columns. NULL values map to nil pointers — no backward compatibility issue.

### 7.2 Repository Layer

**matter_repo.go** — Changes:
1. Add `"seq_no"` to Create's Columns list
2. Add `nextSeqNo(ctx, spaceID)` method
3. Wrap Create in retry-on-duplicate loop

**New file: `activity_repo.go`**:
```go
type ActivityRepo struct { runner dbr.SessionRunner }

func (r *ActivityRepo) Record(ctx context.Context, matterID, actorID, action string, detail interface{}) error
func (r *ActivityRepo) ListByMatter(ctx context.Context, matterID string, cursor *string, limit int) ([]*model.Activity, bool, error)
```

**comment_repo.go** — Add new method (do NOT modify existing Create):
```go
func (r *CommentRepo) CreateDigest(ctx context.Context, c *model.MatterComment) error {
    // Inserts with all digest columns: channel_id, channel_type, source_channel_id, source_msgs, related_uids
}
```

**matter_channel_repo.go** — Add lookup method:
```go
func (r *MatterChannelRepo) FindByMatterAndChannelID(ctx context.Context, matterID, channelID string) (*model.MatterChannel, error)
```

### 7.3 Service Layer

- `matter_svc.go`: Inject ActivityRepo; add activity recording calls after each mutation
- New `extract_svc.go`: LLM prompt assembly for matter creation
- New `digest_svc.go`: LLM prompt assembly for timeline extraction

### 7.4 Handler Layer

- New `extract_handler.go`: POST /matters/extract
- New `digest_handler.go`: POST /matters/:id/timeline
- `router.go`: Register new routes

### 7.5 TxRepos (optional enhancement)

For SetStatus activity recording inside tx (not required for v1):
```go
type TxRepos struct {
    // ... existing fields ...
    Activity *ActivityRepo // optional, for in-tx activity writes
}
```

---

## 8. Safeguards

| Dimension | Strategy |
|-----------|----------|
| Message count | Max 200, reject with 400 if exceeded |
| Content length | Each message truncated to 500 chars server-side |
| Rate limit | In-memory per (matter_id, user_id), 10s cooldown |
| Timeout | 30s request timeout (existing middleware) |
| Auth | Reuse existing auth + space middleware |
| Access | Create: any Space member; Timeline: write access to matter |
| LLM failure | Return 502 (upstream dependency) with sanitized error, no retry |
| Empty extraction | If LLM returns empty/irrelevant, return 422 with explanation |
| body.matter_id ≠ path :id | Return 400 |

---

## 9. Migration SQL (Complete)

```sql
-- 004_digest_and_activities.sql

-- +migrate Up

-- 1. Space-scoped display sequence number
ALTER TABLE matters
  ADD COLUMN seq_no INT UNSIGNED NULL AFTER id,
  ADD UNIQUE KEY uk_space_seq (space_id, seq_no);

-- Backfill existing rows (MySQL 8.0+ window function, safe under strict mode)
UPDATE matters m
JOIN (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at) AS rn
  FROM matters
) ranked ON m.id = ranked.id
SET m.seq_no = ranked.rn;

ALTER TABLE matters MODIFY seq_no INT UNSIGNED NOT NULL;

-- 2. Comment upgrades for digest support
ALTER TABLE matter_comments
  ADD COLUMN channel_id CHAR(36) NULL,
  ADD COLUMN channel_type TINYINT UNSIGNED NULL,
  ADD COLUMN source_channel_id VARCHAR(255) NULL,
  ADD COLUMN source_msgs JSON NULL,
  ADD COLUMN related_uids JSON NULL,
  ADD INDEX idx_comments_matter_channel (matter_id, source_channel_id, created_at),
  ADD CONSTRAINT fk_comments_channel
    FOREIGN KEY (channel_id) REFERENCES matter_channels(id) ON DELETE SET NULL;

-- Clean up legacy index (superseded by compound index above)
ALTER TABLE matter_comments DROP INDEX idx_comments_todo;

-- 3. Activity log
CREATE TABLE IF NOT EXISTS matter_activities (
    id          CHAR(36)    NOT NULL,
    matter_id   CHAR(36)    NOT NULL,
    actor_id    VARCHAR(64) NOT NULL,
    action      VARCHAR(50) NOT NULL,
    detail      JSON        NULL,
    created_at  DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_activity_matter (matter_id, created_at DESC),
    CONSTRAINT fk_activity_matter FOREIGN KEY (matter_id)
        REFERENCES matters(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +migrate Down
DROP TABLE IF EXISTS matter_activities;
ALTER TABLE matter_comments ADD INDEX idx_comments_todo (matter_id);
ALTER TABLE matter_comments DROP FOREIGN KEY fk_comments_channel;
ALTER TABLE matter_comments DROP INDEX idx_comments_matter_channel;
ALTER TABLE matter_comments DROP COLUMN related_uids;
ALTER TABLE matter_comments DROP COLUMN source_msgs;
ALTER TABLE matter_comments DROP COLUMN source_channel_id;
ALTER TABLE matter_comments DROP COLUMN channel_type;
ALTER TABLE matter_comments DROP COLUMN channel_id;
ALTER TABLE matters DROP INDEX uk_space_seq;
ALTER TABLE matters DROP COLUMN seq_no;
```

---

## 10. Route Registration

```go
// In router.go, under matters group:
matters.POST("/extract", extractH.Create)          // Create matter from messages
matters.POST("/:id/timeline", digestH.AddTimeline) // Add digest to matter timeline
```

---

## 11. Implementation Checklist

- [ ] Migration 004 (schema changes + backfill)
- [ ] `internal/model/matter.go` — Add SeqNo field
- [ ] `internal/model/comment.go` — Add digest fields (channel_id, source_channel_id, etc.)
- [ ] `internal/model/activity.go` — New model
- [ ] `internal/llm/client.go` — OpenAI-compatible client with function calling
- [ ] `internal/service/extract_svc.go` — Create matter prompt + parsing
- [ ] `internal/service/digest_svc.go` — Timeline digest prompt + parsing
- [ ] `internal/handler/extract_handler.go` — POST /matters/extract
- [ ] `internal/handler/digest_handler.go` — POST /matters/:id/timeline
- [ ] `internal/repository/matter_repo.go` — Add nextSeqNo() + retry logic + seq_no in Columns
- [ ] `internal/repository/activity_repo.go` — New repo
- [ ] `internal/repository/comment_repo.go` — Add CreateDigest()
- [ ] `internal/repository/matter_channel_repo.go` — Add FindByMatterAndChannelID()
- [ ] `internal/service/matter_svc.go` — Inject ActivityRepo, add post-mutation activity calls
- [ ] `internal/config/config.go` — Add LLM config fields
- [ ] `cmd/main.go` — Wire LLM client, new services, new handlers
- [ ] Rate limiter (in-memory, per matter_id+user_id, 10s cooldown)
- [ ] Integration tests

---

## 12. Difference from smart-summary

| Dimension | smart-summary | matter APIs |
|-----------|--------------|-------------|
| Message source | Auto-fetched from IM DB | User-selected, passed by frontend |
| Processing | Async Worker + MapReduce | Sync, single LLM call |
| Output | General summary + citations | Structured matter/progress extraction |
| Purpose | Retrospective review | Create & track specific matters |
| Tables | Own DB (summary_task, etc.) | Reuses matter_comments |
| Complexity | ~4000 LOC pipeline | ~400 LOC total |

---

## Appendix: Review History

| Version | Reviewer | Key Changes |
|---------|----------|-------------|
| v1 (皮皮) | — | Initial design with separate digests table |
| v1→v2 (皮皮) | 娃爹 | Eliminate new tables where possible, reuse comments |
| v2-final (齐静春) | 齐静春 | seq_no ROW_NUMBER backfill, retry-on-dup strategy, UUID ID convention, compound index, source_channel_id for digest identification |
| v3-final | 李飞飞 | Activity best-effort pattern, channel_id FK lookup flow, creator_uid auth semantics, CommentRepo.CreateDigest, legacy index cleanup, body/path consistency validation |
