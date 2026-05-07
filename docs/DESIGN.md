# Octo Todo — Architecture Design Document (v5)

> **v5 changelog** — consolidates lessons from the v1 implementation:
> - REST-style response envelope (bare object on success, `{error:{code,message,details}}` on failure) — replaces the `{code,msg,data}` wrapper.
> - Cursor pagination returns `has_more` + `next_cursor`; `total` dropped to avoid O(N) COUNT on every list.
> - Composite cursor `(created_at, id)` spelled out — a plain `last_id` cursor is not safe under same-second inserts.
> - `X-Space-ID` header only; query-string alternative removed (leaks into access logs / Referer).
> - Flat `source_channel_id / source_channel_type / source_name` fields on both request and response — matches the DB and list-filter query shape.
> - Enumerated error codes carry `details` (e.g. `VALIDATION_ERROR for invalid status values`).
> - `GET /goals/:id` requires goal assignment, not just space membership.
> - `POST /todos` response includes assignees so clients skip a follow-up `GET`.
> - `PUT /todos/:id` field semantics: absent = unchanged, `""` = clear, non-empty = set.
> - `/health/ready` probes MySQL (and Redis / Octo IM server when wired). `/health` stays a cheap liveness check.
> - `Auth: calls dmworkim POST /v1/auth/verify (user token) or /v1/auth/verify-bot (bot token).

## Overview

### Background

Octo  is an enterprise messaging and collaboration platform. As the platform evolves, productivity tools become essential. To-Dos — the ability to create, assign, track, and complete tasks — is a foundational productivity primitive.

This document defines the architecture for **Octo Todo**: a standalone microservice integrated into the Octo ecosystem. Authentication is delegated to Octo IM server via a new internal auth API (Phase A of the Octo unified auth strategy). Bot and agent automation goes through a unified `octo-cli`. The core model is **simple open/closed todo tracking with optional goal organization**.

### Goals

1. **Independent microservice** — `todo-service` runs standalone with its own MySQL database
2. **Status-driven task model** — Simple open/closed status model (GitHub-style) with assignees (human or bot)
3. **Goal organization (optional) — Todos optionally associate with a goal; sidebar lists goals, selecting one shows its todos
4. **Unified CLI** — `octo-cli` is the single CLI entry point for all Octo services (`octo todo ...`)
5. **Unified auth via Octo IM server** — Token validation through Octo IM server internal API + `dmworkim verify` SDK
6. **Source context tracking** — Every todo records where it was created (group, thread, subsystem), enabling per-conversation todo panels
7. **Space isolation** — All data is scoped to a Space; cross-Space access is forbidden
8. **Chat integration** — Create and update todos from Octo chat conversations
**Web interface** — `todo-web` delivers a React-based todo list UI

### Non-Goals (v1)

- Recurring / repeating todos
- Sub-tasks / checklist items within a todo
- File editing or document collaboration
- Real-time collaborative editing of descriptions
- Custom workflow states or additional status values

## Unified Auth Strategy

### Problem

Octo IM server currently has two auth mechanisms tightly coupled inside its process:
- **User auth**: custom `token` header -> Redis lookup (`tokenPrefix + token` -> `uid@name@role`)
- **Bot auth**: `Authorization: Bearer <bot_token>` -> `robot` table lookup

External microservices (todo-service, future knowledge-service, etc.) cannot call `AuthMiddleware` or `authBot()` directly.

### Solution: Phase A -> Phase B

**Phase A (Now)**: Add 3 internal HTTP endpoints to Octo IM server. All Octo microservices call these endpoints. Ship an `dmworkim verify` Go SDK that wraps the calls + local cache.

**Phase B (Future)**: When 3+ microservices exist, extract these endpoints into a standalone `dmworkim auth-service`. The SDK only changes its target URL; business code untouched.

### Phase A: Octo IM server Internal Endpoints

```
POST /internal/v1/auth/verify
  Request:  { "token": "<user-token>" }
  Response: { "uid": "xxx", "name": "Merlin", "role": "admin" }
  Error:    401 { "msg": "invalid token" }

POST /internal/v1/auth/verify-bot
  Request:  { "bot_token": "bf_xxx" }
  Response: { "robot_id": "xxx", "robot_name": "todo-bot" }
  Error:    401 { "msg": "invalid bot token" }

POST /internal/v1/auth/space-check
  Request:  { "space_id": "xxx", "uid": "xxx" }
  Response: { "is_member": true }
  Error:    403 { "msg": "not a space member" }
```

These endpoints are **internal only** (bound to internal network, not exposed to public).

### dmworkim verify API

```go
// All Octo microservices import this package
import "github.com/Mininglamp-OSS/octo-server verify"

client := octoauth.New(octoauth.Config{
    AuthURL:  "http://Octo IM server:8090/internal/v1",
    CacheTTL: 60 * time.Second,
})

// Gin middleware - one line to integrate
r.Use(client.UserAuthMiddleware())   // validates token header
r.Use(client.SpaceMiddleware())      // validates X-Space-ID header

// Bot routes
botGroup := r.Group("/bot", client.BotAuthMiddleware())
```

The SDK handles:
- Token validation via HTTP call to Octo IM server internal port (:8091)
- Dual token routing: `token` header → user verify, `Authorization: Bearer bf_*` → bot verify
- Read/write split caching: GET requests use 60s local cache, write requests bypass cache for realtime verify
- Unified context injection (`c.Get("uid")`, `c.Get("space_id")`, `c.Get("robot_id")`)
- Graceful fallback when Octo IM server is temporarily unreachable (serve from cache for reads only)

## System Architecture

### Component Diagram

![System Component Diagram](./c1-component.puml)

**Components:**

- **todo-web** — React + TypeScript SPA. Auth tokens obtained from Octo IM, passed via `token` header.
- **octo-cli** — Unified Go CLI. `octo todo` sub-commands call todo-service REST API. Bot agents invoke via `exec`.
- **todo-service** — Go (Gin + wkhttp) REST API. Uses `dmworkim verify` SDK for auth. Owns todo/goal business logic.
- **Octo IM server** — Auth provider (internal endpoints). Notification channel (Bot API for message push).
- **WuKongIM** — Underlying message transport. todo-service sends notifications as a system Bot via Octo IM server Bot API.

### Technology Stack

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| todo-service | Go 1.22+, Gin (wkhttp), dbr | Aligned with Octo IM server ecosystem |
| todo-web | React 18, TypeScript, Vite, Ant Design | Modern SPA stack |
| octo-cli | Go 1.22+, Cobra | Extensible sub-commands |
| Database | MySQL 8 | Aligned with Octo IM server (single DB infrastructure) |
| Cache | Redis 7 | Rate limiting, auth cache, notification queue |
| Auth | dmworkim verify API | Unified auth across all Octo services |

## Data Model

### Entity Relationship Diagram

![ER Diagram](./c2-er.puml)

### Core Entities

#### `goals`

A goal is an optional organizational container for todos.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| id | char(36) | PK, UUID | Unique identifier |
| space\_id | varchar(64) | NOT NULL, INDEX | Space isolation |
| title | varchar(200) | NOT NULL | Goal name |
| description | text | nullable | What this goal aims to achieve |
| creator\_id | varchar(64) | NOT NULL | Creator (Octo UID) |
| archived | tinyint(1) | NOT NULL, default 0 | Soft archive |
| created\_at | datetime(3) | NOT NULL | Creation timestamp |
| updated\_at | datetime(3) | NOT NULL | Last modification |

#### `goal_assignees`

Access control. Assignees can view the goal and create todos under it.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| id | char(36) | PK | Unique identifier |
| goal\_id | char(36) | NOT NULL, FK → goals | Parent goal |
| user\_id | varchar(64) | NOT NULL | Assignee (Octo UID) |
| created\_at | datetime(3) | NOT NULL | When added |

Unique key: `(goal_id, user_id)`.

#### `todos`

The atomic unit. Fixed status + optional goal association + Space scoping.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| id | char(36) | PK, UUID | Unique identifier |
| space\_id | varchar(64) | NOT NULL, INDEX | Space isolation |
| goal\_id | char(36) | nullable, FK | Optional goal |
| title | varchar(500) | NOT NULL | Todo title |
| description | text | nullable | Detailed description |
| creator\_id | varchar(64) | NOT NULL | Creator (Octo UID) |
| status | enum('open','closed') | NOT NULL, default 'open' | Current status |
| deadline | datetime(3) | nullable | Due date |
| remind\_at | datetime(3) | nullable | Reminder time |
| source\_channel\_id | varchar(255) | nullable | Source channel ID (group\_no or group\_no\_\_\_\_short\_id) |
| source\_channel\_type | tinyint unsigned | nullable | Source channel type (2=group, 5=thread, etc.) |
| source\_name | varchar(200) | nullable | Display name of source |
| created\_at | datetime(3) | NOT NULL | Creation timestamp |
| updated\_at | datetime(3) | NOT NULL | Last modification |
| deleted\_at | datetime(3) | nullable | Soft delete |

#### `todo_assignees`

Who is responsible. Supports humans and bots. Per-assignee completion tracking.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| id | char(36) | PK | Unique identifier |
| todo\_id | char(36) | NOT NULL, FK | Parent todo |
| user\_id | varchar(64) | NOT NULL | Assignee (Octo UID) |
| status | enum('pending','done') | NOT NULL, default 'pending' | Completion status |
| completed\_at | datetime(3) | nullable | When marked done |
| created\_at | datetime(3) | NOT NULL | When assigned |

Unique key: `(todo_id, user_id)`.

Note: `assignee_type` (human/bot) is not stored. todo-service queries Octo IM server's robot table via internal API to determine type when needed for display.

#### `todo_comments`

Flat discussion thread on a todo.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| id | char(36) | PK | Unique identifier |
| todo\_id | char(36) | NOT NULL, FK | Parent todo |
| user\_id | varchar(64) | NOT NULL | Author |
| content | text | nullable | Comment body (nullable when comment has attachments only) |
| created\_at | datetime(3) | NOT NULL | Creation timestamp |

A comment must carry either non-empty `content` or at least one attachment (service-enforced).

#### `todo_comment_attachments`

File references attached to a comment. Cascade-deleted with the comment.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| id | char(36) | PK | Unique identifier |
| comment\_id | char(36) | NOT NULL, FK ON DELETE CASCADE | Parent comment |
| file\_url | varchar(1024) | NOT NULL | External storage URL |
| file\_name | varchar(255) | nullable | Original filename |
| file\_size | bigint | nullable | Size in bytes |
| mime\_type | varchar(100) | nullable | MIME type |
| created\_at | datetime(3) | NOT NULL | Upload timestamp |

Service-enforced bounds: max 10 attachments per comment, max 100 MB per attachment.

### Indexes

```sql
-- Space + Goal indexes
CREATE INDEX idx_goals_space_creator ON goals(space_id, creator_id);
CREATE INDEX idx_goal_assignees_user ON goal_assignees(user_id);
CREATE INDEX idx_goal_assignees_goal ON goal_assignees(goal_id);

-- Todo indexes
CREATE INDEX idx_todos_space_status ON todos(space_id, status);
CREATE INDEX idx_todos_goal ON todos(goal_id);
CREATE INDEX idx_todos_creator ON todos(space_id, creator_id);
CREATE INDEX idx_todos_deadline ON todos(space_id, deadline);
CREATE INDEX idx_todos_source ON todos(source_channel_id, source_channel_type);
CREATE INDEX idx_assignees_user ON todo_assignees(user_id);
CREATE INDEX idx_assignees_todo ON todo_assignees(todo_id);
CREATE INDEX idx_assignees_status ON todo_assignees(user_id, status);
CREATE INDEX idx_comment_attachments_comment ON todo_comment_attachments(comment_id);
```

## State Machine

### Todo Status Model

Simple two-state model inspired by GitHub Issues:

| Status | Meaning |
|--------|---------|
| `open` | Needs work (default on creation) |
| `closed` | Done or won't do |

**Rules:**
- Creator or assignee can close or reopen. No state machine, no role-based transition matrix.
- Setting the current status is a no-op (idempotent).
- When a todo is closed, all assignees are auto-marked `done` atomically in the same transaction.
- Invalid status values return `VALIDATION_ERROR` (400).

**Assignee status (independent):**
- `pending` -> `done`: Assignee marks self done
- `done` -> `pending`: Assignee reverts

## API Design

### Authentication

All requests must include the `token` header (user) or `Authorization: Bearer <bot_token>` (bot). todo-service validates via `dmworkim verify` SDK which calls Octo IM server internal endpoints.

Space context is passed via the `X-Space-ID` header **only**. The query-string alternative (`?space_id=...`) is rejected — space IDs must not leak into access logs or Referer headers.

```
token: <octo-user-token>
X-Space-ID: <space-id>
```

During Phase A, `Auth: calls dmworkim POST /v1/auth/verify (user token) or /v1/auth/verify-bot (bot token).

### Response Envelope

**Success** responses return the resource as a bare JSON object (or array) — no wrapper. HTTP status code (`200`, `201`, `204`) carries success. Clients read fields directly: `response.status`, not `response.data.status`.

**Error** responses always take the shape:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "status must be 'open' or 'closed'",
  }
}
```

`code` is a stable machine-readable enum (see Error Codes below). `message` is a human-readable string safe to render verbatim. `details` is optional and only populated for codes where it is specified (currently only for VALIDATION_ERROR).

### RESTful Endpoints

Base path: `/api/v1`

#### Goal CRUD

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | /goals | Create goal | Required |
| GET | /goals | List goals (in current Space) | Required (space member) |
| GET | /goals/:id | Get goal detail (metadata + assignees) | Required (goal member) |
| PUT | /goals/:id | Update goal | Required (owner) |
| DELETE | /goals/:id | Archive goal | Required (owner) |
| POST | /goals/:id/assignees | Add assignees | Required (creator) |
| DELETE | /goals/:id/assignees/:uid | Remove assignee | Required (creator) |

Goal detail (`GET /goals/:id`) returns metadata and assignees, scoped to goal assignees. Non-assignees in the same Space receive `403 FORBIDDEN`.

#### Todo CRUD

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | /todos | Create todo | Required |
| GET | /todos | List todos (filterable, cursor-paginated) | Required |
| GET | /todos/:id | Get todo detail | Required |
| PUT | /todos/:id | Update todo (see field semantics below) | Required (creator) |
| PUT | /todos/:id/status | Transition status | Required (creator or assignee) |
| DELETE | /todos/:id | Soft delete | Required (creator) |
| POST | /todos/:id/assignees | Add assignees | Required (creator) |
| DELETE | /todos/:id/assignees/:uid | Remove assignee | Required (creator) |
| PUT | /todos/:id/assignee-status | Update own completion | Required (assignee) |

**`PUT /todos/:id` field semantics** (applies to `description`, `goal_id`, `deadline`, `remind_at`):

| Request body form | Meaning |
|-------------------|---------|
| field absent | leave current value unchanged |
| `"field": null` or `"field": ""` | clear the field (set column to NULL) |
| `"field": "<value>"` | set to that value |

`title` is required on update and does not support clearing (it is `NOT NULL`).

#### Comments

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| GET | /todos/:id/comments | List comments (with attachments) | Required |
| POST | /todos/:id/comments | Add comment (optionally with attachments) | Required |
| DELETE | /todos/:id/comments/:cid | Delete comment (cascades attachments) | Required (author) |

A comment request body accepts `content` (string, optional) and `attachments` (array, optional). At least one must be non-empty. Each attachment carries `file_url` (required), plus optional `file_name`, `file_size`, `mime_type`. Max 10 attachments per comment, max 100 MB per attachment, max 10,000 chars of content.

### Pagination

All list endpoints use **cursor-based** pagination. A composite cursor `(created_at, id)` is encoded as an opaque base64 string — a plain `last_id` cursor would skip or repeat rows when two items share the same creation second. The server sorts `ORDER BY created_at DESC, id DESC` and applies `WHERE (created_at < ?C OR (created_at = ?C AND id < ?I))`.

```
GET /api/v1/todos?limit=20&cursor=<opaque>
```

Response:

```json
{
  "data": [ /* resource objects */ ],
  "pagination": {
    "has_more": true,
    "next_cursor": "eyJjIjoiMjAyNi0wNC0yNVQxMDowNTowMFoiLCJpIjoidDFiMmMzZDQifQ=="
  }
}
```

- `limit` defaults to 20 and is clamped to `[1, 100]`.
- The server fetches `limit + 1` rows internally; if the extra row exists, `has_more=true` and it is stripped from `data`. This means `has_more` is exact — clients stop paging as soon as it is `false`.
- `next_cursor` is only emitted when `has_more=true`.
- **No `total` field.** Cursor-paginated lists do not carry a row count — computing it would require an O(N) COUNT on every page. Clients that need a count should maintain it client-side.

### Request / Response Examples

#### Create Todo

```
POST /api/v1/todos
token: <octo-user-token>
X-Space-ID: sp_abc123

{
  "title": "Complete requirements document",
  "goal_id": "g1a2b3c4-d5e6-7890-abcd-ef1234567890",
  "assignee_ids": ["d0bb36669625432898ab339c78b8db94", "bot_reviewer_uid"],
  "source_channel_id": "g1234567890",
  "source_channel_type": 2,
  "source_name": "#product-team",
  "deadline": "2026-04-26T18:00:00+08:00"
}
```

Response (`201 Created`) — bare object:

```json
{
  "id": "t1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "space_id": "sp_abc123",
  "goal_id": "g1a2b3c4-d5e6-7890-abcd-ef1234567890",
  "title": "Complete requirements document",
  "creator_id": "d0bb36669625432898ab339c78b8db94",
  "status": "open",
  "assignees": [
    {"user_id": "d0bb36669625432898ab339c78b8db94", "status": "pending"},
    {"user_id": "bot_reviewer_uid", "status": "pending"}
  ],
  "source_channel_id": "g1234567890",
  "source_channel_type": 2,
  "source_name": "#product-team",
  "deadline": "2026-04-26T18:00:00+08:00",
  "created_at": "2026-04-25T10:05:00+08:00"
}
```

The `assignees[].user_id` is the raw Octo UID. Display-time enrichment (name, `is_bot`) is the web/CLI client's responsibility — todo-service does not couple itself to Octo IM server's user/robot tables at read time.

#### Transition Status

```
PUT /api/v1/todos/t1b2c3d4/status
token: <octo-user-token>
X-Space-ID: sp_abc123

{ "status": "closed" }
```

Success (`200 OK`):
```json
{
  "id": "t1b2c3d4",
  "status": "closed",
  "assignees": [ /* ... */ ]
}
```

Invalid transition (`400 Bad Request`):
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "status must be 'open' or 'closed'",
  }
}
```

#### Get Goal (Kanban View)

```
GET /api/v1/goals/g1a2b3c4
token: <octo-user-token>
X-Space-ID: sp_abc123
```

Response — goal metadata and assignees:
```json
{
  "id": "g1a2b3c4",
  "title": "Q2 Product Launch",
  "description": "Ship v2.0 by June 30",
  "creator_id": "u_merlin",
  "assignees": [
    { "id": "ga1", "goal_id": "g1a2b3c4", "user_id": "u_merlin" },
    { "id": "ga2", "goal_id": "g1a2b3c4", "user_id": "u_feifei" }
  ]
}
```

To list todos under this goal, use `GET /api/v1/todos?goal_id=g1a2b3c4` with cursor pagination.

#### List Todos by Source (Chat Panel Query)

```
GET /api/v1/todos?source_channel_id=g1234567890&source_channel_type=2&limit=20
token: <octo-user-token>
X-Space-ID: sp_abc123
```

This powers the per-conversation todo panel in Octo chat windows.

### Error Codes

All error responses carry a stable `code`. Clients should branch on `code`, not on `message` (which is human-readable and may change).

| Code | HTTP | When | `details` |
|------|------|------|-----------|
| `UNAUTHORIZED` | 401 | Missing or invalid `token` / `Authorization` | — |
| `FORBIDDEN` | 403 | Authenticated but lacks the required role (non-creator edit, non-creator goal update, non-assignee goal detail) | — |
| `VALIDATION_ERROR` | 400 | Request body / params failed binding or field validation | `{field, reason}` when available |
| `GOAL_NOT_FOUND` | 404 | Goal id does not exist in the current Space | — |
| `TODO_NOT_FOUND` | 404 | Todo id does not exist in the current Space (or is soft-deleted) | — |
| `ASSIGNEE_NOT_FOUND` | 404 | Assignee row does not exist for the specified `(todo_id, user_id)` | — |
| `VALIDATION_ERROR` | 400 | Invalid status value (must be 'open' or 'closed') | `` |
| `DUPLICATE_ASSIGNEE` | 409 | `(todo_id, user_id)` already exists | — |
| `SPACE_FORBIDDEN` | 403 | Caller is not a member of the target Space (enforced by SpaceMiddleware once Phase A auth is wired) | — |
| `RATE_LIMITED` | 429 | Per-endpoint or global limit exceeded | `{retry_after_sec}` |
| `INTERNAL_ERROR` | 500 | Unhandled server error | — |

Cross-Space access to a resource returns `TODO_NOT_FOUND` / `GOAL_NOT_FOUND`, never `FORBIDDEN` — clients cannot distinguish "wrong space" from "does not exist", closing a tenancy information-leak.

## Notification Design

todo-service sends notifications as a **system Bot** via Octo IM server's Bot API:

```
POST /v1/bot/sendMessage
Authorization: Bearer <todo-bot-token>

{
  "channel_id": "<target_group_no>",
  "channel_type": 2,
  "payload": { "type": 1, "content": "Task 'Review PR' closed by Merlin" }
}
```

todo-service registers itself as a Bot in Octo IM server (via BotFather). This Bot identity is used for all notifications.

| Event | Notification target |
|-------|-------------------|
| TodoCreated | All assignees |
| TodoStatusChanged | All assignees |
| TodoCompleted (done) | Goal owner (if goal) + creator |
| AssigneeAdded | Added user/bot |
| DeadlineReminder | Pending assignees |

Deduplication: if a user is both creator, assignee, and goal owner, they receive **one** notification per event (deduplicated by uid before send).

## octo-cli Design

### Authentication Flow

```bash
# Human user: OAuth-style login via Octo IM server OpenAPI
octo auth login
# -> Opens browser -> Octo login -> authcode -> access_token
# -> Stored in ~/.config/octo/config.yaml

# Bot: direct token configuration
octo auth set-bot-token <bf_xxx>
```

Under the hood, `octo auth login` uses Octo IM server's existing OpenAPI flow:
1. `GET /v1/openapi/authcode?app_id=octo-cli` (authenticated)
2. `GET /v1/openapi/access_token?authcode=xxx&app_key=xxx` (exchange)
3. Store access\_token locally

### Command Structure

```
octo <service> <command> [flags]

Services:
  auth        Authentication management
  todo        Task management
  goal        Goal management
  (future)    knowledge, workflow, ...

Global Flags:
  --server    Server URL (default: from config)
  --token     Override token
  --space     Space ID (default: from config)
  --format    text|json (default: text)
  --quiet     Minimal output (scripting)
```

### Usage Examples

```bash
# Create a goal
octo goal create "Q2 Launch" \
  --desc "Ship v2.0 by June 30" \
  --space sp_abc123

# Create a todo under a goal
octo todo create "Review PR #42" \
  --goal "Q2 Launch" \
  --assign d0bb366,bot_reviewer

# Transition status
octo todo close t1b2c3d4

# List todos in current space
octo todo list --assignee me --status open

# Bot usage (JSON output)
octo todo list --format json --assignee bot_reviewer
```

## todo-web Design

### Pages

| Page | Path | Description |
|------|------|-------------|
| My Todos | / | Cross-goal list (filterable, paginated) |
| Goal List | /goals | Goals with progress stats |
| Goal Board | /goals/:id | Kanban: todos by status columns |
| Todo Detail | /todos/:id | Full detail with assignees, comments |

### Kanban UX

- **5 status columns**: Draft, Planned, In Progress, Done, Cancelled
- Todo list with open/closed toggle and status badges
- **Bot assignees**: robot icon distinct from human avatars
- **Space scoping**: space selector in top nav

### Chat-Embedded Todo Panel

In Octo chat windows, a toggle button opens a todo side panel:
- Queries `GET /api/v1/todos?source_channel_id=<current>&source_channel_type=<type>`
- Shows todos list with quick-create and status badges
- Auto-refreshes via polling

## Service Internal Architecture

### Directory Layout

```
todo-service/
|-- cmd/main.go
|-- internal/
|   |-- config/config.go
|   |-- model/
|   |   |-- goal.go
|   |   |-- todo.go            # open/closed status model
|   |   |-- assignee.go
|   |   |-- comment.go
|   |   |-- comment_attachment.go
|   |-- repository/            # dbr-based data access
|   |   |-- goal_repo.go
|   |   |-- todo_repo.go
|   |   |-- assignee_repo.go
|   |-- service/
|   |   |-- goal_svc.go
|   |   |-- todo_svc.go        # status + permissions
|   |   |-- notification_svc.go
|   |-- handler/
|   |   |-- goal_handler.go
|   |   |-- todo_handler.go
|   |   |-- middleware.go       # dmworkim verify integration
|   |-- integration/
|       |-- octo_bot.go       # Bot API client for notifications
|-- migrations/                 # MySQL migrations (hand-written SQL)
|-- Dockerfile
|-- docker-compose.yaml
```

## Deployment

### Docker Compose

```yaml
services:
  todo-service:
    build: ./todo-service
    ports: ["8080:8080"]
    environment:
      - MYSQL_DSN=todo:todo@tcp(mysql:3306)/octo_todo?charset=utf8mb4&parseTime=true
      - REDIS_URL=redis://redis:6379/0
      - AUTH_URL=http://Octo IM server:8090/internal/v1
      - BOT_TOKEN=bf_todo_system_bot
    depends_on: [mysql, redis]

  todo-web:
    build: ./todo-web
    ports: ["3000:80"]

  mysql:
    image: mysql:8.0
    environment:
      MYSQL_DATABASE: octo_todo
      MYSQL_USER: todo
      MYSQL_PASSWORD: todo
      MYSQL_ROOT_PASSWORD: root
    volumes: ["mysqldata:/var/lib/mysql"]

  redis:
    image: redis:7-alpine

volumes:
  mysqldata:
```

### Health Checks

```
GET /health        -> 200 {"status":"ok"}        # liveness: process is up, no I/O
GET /health/ready  -> 200 {"status":"ready"}     # readiness: MySQL Ping succeeds (Redis + Octo IM server auth added when wired)
                   -> 503 {"error":{"code":"NOT_READY","message":"...","details":{"mysql":"<err>"}}}
```

`/health` must stay cheap (no DB I/O) so a saturated DB doesn't restart-loop healthy pods. `/health/ready` is what k8s readiness probes should target — failing it pulls the Pod out of the Service's endpoints until dependencies recover.

## Repository Structure

```
codex.example.com/octo/octo-todo/       # todo-service + todo-web + docs
codex.example.com/octo/octo-cli/        # unified CLI (separate repo)
codex.example.com/octo/dmworkim verify/ # auth SDK (separate repo)
```

## Implementation Phases

### Phase 1 — Auth + Core Service + CLI (Week 1-2)

- Octo IM server: implement 3 internal auth endpoints
- dmworkim verify API (Go package)
- MySQL schema + migrations
- Goal CRUD API + Space isolation
- Todo CRUD API + open/closed status + pagination
- Assignee management
- octo-cli: `octo auth` + `octo todo` + `octo goal`
- Tests (target: 80% coverage)
- Docker Compose

### Phase 2 — Web UI (Week 2-3)

- React SPA (Vite + Ant Design)
- Octo token auth flow
- Goal list + todo list with open/closed status toggle
- My Todos list (cross-goal, filterable)
- Todo detail modal
- Responsive

### Phase 3 — Chat Integration + Notifications (Week 3-4)

- Register todo-system-bot in Octo IM server
- Notification service (Bot API push + deduplication)
- Chat command support (`@todo-bot /todo create xxx`)
- Chat-embedded todo panel (source context query)
- Deadline reminder scheduler

### Phase 4 — Hardening (Week 4)

- Rate limiting (reuse Octo IM server's Redis-based approach)
- Security audit
- Performance testing
- CI/CD pipeline
- Production deployment guide

## Security Considerations

1. **Authentication** — Delegated to Octo IM server via internal API. No local token issuance.
2. **Space isolation** — All queries filtered by `space_id`. Cross-Space access returns 403.
3. **Authorization** — Goal creator manages assignees; todo creator manages todo; assignees can transition and mark completion.
4. **Input validation** — go-playground/validator. Title max 500 chars, description max 10000 chars.
5. **SQL injection** — dbr parameterized queries exclusively.
6. **Rate limiting** — Redis sliding window, aligned with Octo IM server's existing approach.
7. **Soft delete** — Todos never hard-deleted; audit trail via `deleted_at`.
8. **Internal API security** — `/internal/v1/auth/*` bound to internal network only. Two-layer enforcement:
   - **Nginx layer**: `location /internal/ { allow 192.0.2.0/24; allow 198.51.100.0/24; deny all; }`
   - **Octo IM server layer**: Internal routes bind to separate port `:8091` (not `:8090`), accessible only from internal network.
   Public port `:8090` does NOT serve `/internal/*` paths.
9. **Auth cache policy** — dmworkim verify API uses read/write split caching:
   - Read operations (GET): 60s local cache TTL
   - Write operations (PUT/POST/DELETE): realtime verify, no cache
   This prevents stale-token phantom writes while keeping read performance high.
10. **Dual token routing** — SDK middleware detects token type automatically:
    - `token` header present → user auth path (`/internal/v1/auth/verify`)
    - `Authorization: Bearer bf_*` → bot auth path (`/internal/v1/auth/verify-bot`)
    Both paths set `uid` + `role` ("user" or "bot") in gin context uniformly.

## Appendix

### Appendix A: Comparison with Existing Tools

| Feature | WeCom To-Dos | Trello / Notion | Octo Todo |
|---------|-------------|----------------|-----------|
| Goal organization | No | Partial | Yes (optional grouping) |
| Simple open/closed status | No | No | Yes |
| Space isolation | N/A | Workspace | Yes |
| Create from chat | Yes | No | Yes |
| Assign to bots | No | No | Yes |
| Per-user completion | Yes | No | Yes |
| Unified CLI | No | No | Yes (octo-cli) |
| Auth via IM platform | Yes | No | Yes (Octo IM server) |
| Self-hosted | No | No | Yes |

### Appendix B: Rate Limits

| Endpoint Category | Limit | Window |
|------------------|-------|--------|
| Goal write | 30 | 1 min |
| Goal read | 300 | 1 min |
| Todo write | 100 | 1 min |
| Todo read | 300 | 1 min |
| Comment write | 60 | 1 min |
| Assignee update | 100 | 1 min |
| Global per-user | 500 | 1 min |

### Appendix C: Status Model

```
    +------+      close      +--------+
    | open | ───────────▸ | closed |
    +------+ ◂─────────── +--------+
                 reopen
```

Creator or assignee can close or reopen. No state machine, no role matrix.

### Appendix D: Octo IM server Channel Types Reference

| Value | Type | Example channel\_id |
|-------|------|-------------------|
| 1 | Person (DM) | uid |
| 2 | Group | group\_no |
| 5 | Thread (CommunityTopic) | group\_no\_\_\_\_short\_id |
