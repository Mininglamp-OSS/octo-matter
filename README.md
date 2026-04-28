# Octo Todo Service

Simple task management microservice for the Octo ecosystem.

## Core Concepts

- **Todo** — Atomic task unit with two statuses: `open` and `closed`. Creator or assignee can close or reopen
- **Goal** — Optional organizational container. Sidebar lists all goals; selecting a goal shows its todos
- **Space** — All data is scoped to a Space for multi-tenant isolation
- **Source Context** — Each todo records where it was created (group channel, thread, etc.)

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
     └─────────────┘
```

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.25+ |
| HTTP | Gin |
| ORM | gocraft/dbr/v2 |
| Database | MySQL 8 |
| UUID | google/uuid |
| Validation | go-playground/validator/v10 |

## Project Structure

```
todos/
├── cmd/main.go                    # Entry point
├── internal/
│   ├── auth/middleware.go         # Auth middleware (stub/jwt/bot modes)
│   ├── config/config.go          # Environment-based configuration
│   ├── model/
│   │   ├── todo.go               # Todo model (open/closed status)
│   │   ├── goal.go               # Goal + GoalAssignee models
│   │   ├── assignee.go           # TodoAssignee model
│   │   ├── comment.go            # TodoComment model
│   │   └── attachment.go         # TodoAttachment model
│   ├── repository/               # Data access layer (dbr)
│   ├── service/                  # Business logic layer
│   └── handler/                  # HTTP handler layer (Gin)
├── migrations/
│   ├── 001_init.up.sql
│   └── 001_init.down.sql
├── docs/DESIGN.md                # Architecture design document
├── Dockerfile
├── docker-compose.yaml
└── CLAUDE.md                     # AI coding conventions
```

## Current Implementation Status

The following features are **designed** in `docs/DESIGN.md` but **not yet implemented**:

- **Redis caching** — declared in docker-compose and config but not wired
- **Rate limiting** — sliding window design exists but no middleware
- **Chat notifications** — Bot API push designed but not built

## Quick Start

### Docker Compose (recommended)

```bash
git clone git@github.com:Mininglamp-OSS/octo-matter.git
cd todos
docker compose up -d
curl http://localhost:8080/health
```

### Local Development

```bash
# Prerequisites: Go 1.25+, MySQL 8

git clone git@github.com:Mininglamp-OSS/octo-matter.git
cd todos

# Create database and run migrations
mysql -u root -p -e "CREATE DATABASE IF NOT EXISTS octo_todo"
mysql -u root -p octo_todo < migrations/001_init.up.sql

# Set environment variables
export MYSQL_DSN="root:root@tcp(localhost:3306)/octo_todo?charset=utf8mb4&parseTime=true"
export SERVER_PORT="8080"

# Build and run
go build -o todo-service ./cmd/main.go
./todo-service

# Run tests
go test ./... -v
```

## API Reference

All endpoints require:
- Authentication header (token for users, Authorization: Bot for bots)
- `X-Space-ID` header — Space ID for data isolation (user auth; bot auth resolves automatically)

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness check |
| GET | `/health/ready` | Readiness check (probes MySQL) |

### Goals

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/goals` | Create goal |
| GET | `/api/v1/goals` | List goals (paginated) |
| GET | `/api/v1/goals/:id` | Get goal detail + assignees |
| PUT | `/api/v1/goals/:id` | Update goal (creator only) |
| DELETE | `/api/v1/goals/:id` | Archive goal (creator only) |
| POST | `/api/v1/goals/:id/assignees` | Add assignee (creator only) |
| DELETE | `/api/v1/goals/:id/assignees/:uid` | Remove assignee (creator only) |

### Todos

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/todos` | Create todo |
| GET | `/api/v1/todos` | List todos (paginated, filterable) |
| GET | `/api/v1/todos/:id` | Get todo detail + assignees |
| PUT | `/api/v1/todos/:id` | Update todo (creator only) |
| PUT | `/api/v1/todos/:id/status` | Set status: open or closed |
| DELETE | `/api/v1/todos/:id` | Soft delete (creator only) |
| POST | `/api/v1/todos/:id/assignees` | Add assignee (creator only) |
| DELETE | `/api/v1/todos/:id/assignees/:uid` | Remove assignee (creator only) |

#### List Filters

```
GET /api/v1/todos?status=open&assignee_id=me&goal_id=xxx&limit=20&cursor=xxx
```

| Parameter | Description |
|-----------|-------------|
| `status` | Filter by status (`open` or `closed`) |
| `goal_id` | Filter by goal |
| `assignee_id` | Filter by assignee (`me` = current user) |
| `creator_id` | Filter by creator (`me` = current user) |
| `source_channel_id` | Filter by source channel |
| `source_channel_type` | Filter by channel type (2=group, 5=thread) |
| `q` | Text search on title |
| `limit` | Page size (default 20, max 100) |
| `cursor` | Cursor for pagination |

#### Set Status

```
PUT /api/v1/todos/:id/status
{ "status": "closed" }
```

Creator or assignee can set status to `open` or `closed`. Idempotent — setting the current status is a no-op. 

### Comments & Attachments

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/todos/:id/comments` | Add comment |
| GET | `/api/v1/todos/:id/comments` | List comments |
| DELETE | `/api/v1/todos/:id/comments/:cid` | Delete comment (author only) |
| POST | `/api/v1/todos/:id/attachments` | Add attachment (http/https URL) |
| GET | `/api/v1/todos/:id/attachments` | List attachments |
| DELETE | `/api/v1/todos/:id/attachments/:aid` | Delete attachment (uploader only) |

### Error Response Format

```json
{
  "error": {
    "code": "FORBIDDEN",
    "message": "only creator or assignee can change status"
  }
}
```

## Authentication

Dual-path auth via dmworkim verify API:

| Header | Path | Use case |
|--------|------|----------|
| `token` | User token → POST dmworkim /v1/auth/verify | Human users (dmwork-web) |
| `Authorization: Bearer <bot_token>` | Bot token → POST dmworkim /v1/auth/verify-bot | Bot/agent automation |

Bot-owner visibility: users see their bots' goals/todos, bots see their owner's.

Config: `DMWORKIM_URL` (dmworkim base URL) + `WUKONGIM_URL` (notification delivery).

## Design Document

Full architecture design with data model, API specification, auth strategy:

→ **[docs/DESIGN.md](docs/DESIGN.md)**

## Related Repositories

| Repository | Description |
|-----------|-------------|
| [Mininglamp-OSS/octo-server](https://github.com/Mininglamp-OSS/octo-server) | Core IM platform (auth verify API) |
| [Mininglamp-OSS/octo-cli](https://github.com/Mininglamp-OSS/octo-cli) | Unified Octo CLI |
| [Mininglamp-OSS/octo-web](https://github.com/Mininglamp-OSS/octo-web) | Web frontend (todo-web module) |

## License

Apache-2.0
