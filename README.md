# Octo Todo Service

Goal-driven kanban task management microservice for the Octo  ecosystem.

## Core Concepts

- **Todo** — Atomic task unit with a fixed state machine: `draft → planned → in_progress → done / cancelled` (+ reactivate: `cancelled → draft`)
- **Goal** — Optional organizational container. A goal's kanban view groups its todos by status columns
- **Space** — All data is scoped to a Space for multi-tenant isolation
- **Source Context** — Each todo records where it was created (group channel, thread, etc.) for per-conversation task panels

## Architecture

```
┌──────────┐  ┌──────────┐  ┌──────────┐
│ todo-web │  │ octo-cli │  │ octo     │
│ (React)  │  │ (Go CLI) │  │ Bot      │
└────┬─────┘  └────┬─────┘  └────┬─────┘
     │             │              │
     └──────┬──────┘──────────────┘
            │  token header + X-Space-ID
            v
     ┌──────────────┐     ┌──────────────┐
     │ todo-service  │────>│  Octo IM    │
     │ (Go/Gin/dbr) │     │ internal API │
     └──────┬───────┘     └──────────────┘
            │
     ┌──────┴──────┐
     │   MySQL 8   │
     │   Redis 7   │
     └─────────────┘
```

## Tech Stack

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| Language | Go 1.22+ | Aligned with Octo IM |
| HTTP | Gin | Aligned with Octo IM (wkhttp) |
| ORM | gocraft/dbr/v2 | Aligned with Octo IM |
| Database | MySQL 8 | Aligned with Octo IM |
| Cache | Redis 7 | Rate limiting, auth cache |
| UUID | google/uuid | Application-layer generation |
| Validation | go-playground/validator/v10 | Request validation |
| Auth | octo-auth-client SDK (planned) | Unified auth via Octo IM internal API |

## Project Structure

```
todos/
├── cmd/
│   └── main.go                    # Entry point: config → db → repos → services → handlers → server
├── internal/
│   ├── auth/
│   │   └── middleware.go          # Auth middleware (token + space_id headers)
│   ├── config/
│   │   └── config.go             # Environment-based configuration
│   ├── model/
│   │   ├── goal.go               # Goal + GoalMember models
│   │   ├── todo.go               # Todo model + state machine (ValidTransitions, CanTransition)
│   │   ├── todo_test.go          # State machine unit tests (6 test cases)
│   │   ├── assignee.go           # TodoAssignee model
│   │   ├── comment.go            # TodoComment model
│   │   └── attachment.go         # TodoAttachment model
│   ├── repository/               # Data access layer (dbr, parameterized queries)
│   │   ├── db.go                 # MySQL dbr session factory
│   │   ├── goal_repo.go          # Goal CRUD + member management
│   │   ├── todo_repo.go          # Todo CRUD + cursor pagination + kanban grouping
│   │   ├── assignee_repo.go      # Assignee CRUD + bulk operations
│   │   ├── comment_repo.go       # Comment CRUD
│   │   └── attachment_repo.go    # Attachment CRUD
│   ├── service/                  # Business logic layer
│   │   ├── goal_svc.go           # Goal lifecycle + kanban view + member management
│   │   ├── todo_svc.go           # Todo lifecycle + state machine enforcement + permissions
│   │   ├── comment_svc.go        # Comment management
│   │   └── attachment_svc.go     # Attachment management
│   └── handler/                  # HTTP handler layer (Gin)
│       ├── router.go             # Route registration + health endpoints
│       ├── resp.go               # Unified response helpers (ok/fail/created)
│       ├── goal_handler.go       # Goal HTTP handlers
│       ├── todo_handler.go       # Todo HTTP handlers (status transition + pagination)
│       ├── comment_handler.go    # Comment HTTP handlers
│       └── attachment_handler.go # Attachment HTTP handlers
├── migrations/
│   ├── 001_init.up.sql           # Create all tables (6 tables + indexes)
│   └── 001_init.down.sql         # Drop all tables
├── docs/
│   └── DESIGN.md                 # Full architecture design document (v4)
├── Dockerfile                    # Multi-stage build (golang:1.22-alpine → alpine:3.19)
├── docker-compose.yaml           # MySQL + Redis + todo-service
├── go.mod
├── go.sum
├── CLAUDE.md                     # AI coding conventions
└── README.md                     # This file
```

## Quick Start

### Docker Compose (recommended)

```bash
git clone git@github.com:Mininglamp-OSS/octo-matter.git
cd todos
docker compose up -d

# Service available at http://localhost:8080
curl http://localhost:8080/health
```

### Local Development

```bash
# Prerequisites: Go 1.22+, MySQL 8, Redis 7

# Clone
git clone git@github.com:Mininglamp-OSS/octo-matter.git
cd todos

# Create database and run migrations
mysql -u root -p -e "CREATE DATABASE IF NOT EXISTS octo_todo"
mysql -u root -p octo_todo < migrations/001_init.up.sql

# Set environment variables
export MYSQL_DSN="root:root@tcp(localhost:3306)/octo_todo?charset=utf8mb4&parseTime=true"
export REDIS_URL="redis://localhost:6379/0"
export AUTH_URL="http://localhost:8090/internal/v1"
export SERVER_PORT="8080"

# Build and run
go build -o todo-service ./cmd/main.go
./todo-service

# Run tests
go test ./... -v
```

## API Reference

All endpoints require:
- `token` header — Octo user token for authentication
- `X-Space-ID` header — Space ID for data isolation

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness check |
| GET | `/health/ready` | Readiness check (DB + Redis) |

### Goals

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/goals` | Create goal |
| GET | `/api/v1/goals` | List goals in current Space |
| GET | `/api/v1/goals/:id` | Get goal with kanban view (todos grouped by status) |
| PUT | `/api/v1/goals/:id` | Update goal (owner only) |
| DELETE | `/api/v1/goals/:id` | Archive goal (owner only) |
| POST | `/api/v1/goals/:id/members` | Add member (owner only) |
| DELETE | `/api/v1/goals/:id/members/:uid` | Remove member (owner only) |

### Todos

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/todos` | Create todo (with optional assignee_ids, goal_id, source) |
| GET | `/api/v1/todos` | List todos (paginated, filterable) |
| GET | `/api/v1/todos/:id` | Get todo detail (includes allowed_transitions) |
| PUT | `/api/v1/todos/:id` | Update todo (creator only) |
| PUT | `/api/v1/todos/:id/status` | Transition status (returns new allowed_transitions) |
| DELETE | `/api/v1/todos/:id` | Soft delete (creator only) |
| POST | `/api/v1/todos/:id/assignees` | Add assignee (creator only) |
| DELETE | `/api/v1/todos/:id/assignees/:uid` | Remove assignee (creator only) |
| PUT | `/api/v1/todos/:id/assignee-status` | Update own completion status (assignee only) |

#### List Filters

```
GET /api/v1/todos?status=in_progress&assignee_id=xxx&goal_id=xxx&source_channel_id=xxx&source_channel_type=2&limit=20&cursor=xxx
```

| Parameter | Description |
|-----------|-------------|
| `status` | Filter by status (draft/planned/in_progress/done/cancelled) |
| `goal_id` | Filter by goal |
| `assignee_id` | Filter by assignee |
| `creator_id` | Filter by creator |
| `source_channel_id` | Filter by source channel (for chat panel) |
| `source_channel_type` | Filter by channel type (2=group, 5=thread) |
| `q` | Text search on title |
| `limit` | Page size (default 20, max 100) |
| `cursor` | Cursor for pagination (last item ID) |

#### Status Transition Response

```json
{
  "data": {
    "id": "...",
    "status": "in_progress",
    "allowed_transitions": ["done", "cancelled"],
    "assignees": [...]
  }
}
```

### Comments & Attachments

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/todos/:id/comments` | Add comment |
| GET | `/api/v1/todos/:id/comments` | List comments |
| DELETE | `/api/v1/todos/:id/comments/:cid` | Delete comment (author only) |
| POST | `/api/v1/todos/:id/attachments` | Add attachment |
| GET | `/api/v1/todos/:id/attachments` | List attachments |
| DELETE | `/api/v1/todos/:id/attachments/:aid` | Delete attachment (uploader only) |

### Error Response Format

```json
{
  "error": {
    "code": "Forbidden",
    "message": "only the creator can update a todo"
  }
}
```

## State Machine

```
                 ┌───────┐
                 │ draft │◄─────────┐
                 └───┬───┘          │
                     │          reactivate
                plan │              │
                     ▼              │
               ┌─────────┐    ┌───────────┐
               │ planned  │───▸│ cancelled │
               └────┬────┘    └───────────┘
                    │               ▲
              start │               │
                    ▼               │
             ┌──────────────┐       │
             │ in_progress  │───────┘
             └──────┬───────┘
                    │
            complete│
                    ▼
                ┌──────┐
                │ done │
                └──────┘
```

**Permissions:** Creator can trigger all transitions. Assignees can only `start` (planned→in_progress) and `complete` (in_progress→done).

## Authentication

todo-service uses Octo IM's internal auth API (Phase A of Octo unified auth strategy):

```
POST /internal/v1/auth/verify        → {uid, name, role}
POST /internal/v1/auth/verify-bot    → {robot_id, robot_name}
POST /internal/v1/auth/space-check   → {is_member: bool}
```

Currently using stub middleware. Full integration via `octo-auth-client` SDK planned.

## Design Document

Full architecture design (v4) with data model, API specification, auth strategy, deployment guide:

→ **[docs/DESIGN.md](docs/DESIGN.md)**

## Related Repositories

| Repository | Description |
|-----------|-------------|
| [Mininglamp-OSS/octo-matter](https://github.com/Mininglamp-OSS/octo-matter) | This service (todo-service) |
| [Mininglamp-OSS/octo-cli](https://github.com/Mininglamp-OSS/octo-cli) | Unified Octo CLI |
| [Mininglamp-OSS/Octo IM](https://github.com/Mininglamp-OSS/Octo IM) | Core IM platform |

## License

Proprietary — Octo
