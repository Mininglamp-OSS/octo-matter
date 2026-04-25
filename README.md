# Octo Todo Service

Task management microservice for the Octo (dmwork) ecosystem. Goal-driven kanban with status-driven todos, Space isolation, and per-conversation task panels.

## Architecture

- **Status machine**: `draft → planned → in_progress → done / cancelled` (+ reactivate: `cancelled → draft`)
- **Goal = optional kanban**: todos optionally associate with a goal; kanban view groups by status
- **Space isolation**: all data scoped to a Space
- **Source context**: todos track where they were created (group/thread)

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.22+ |
| HTTP | Gin |
| Database | MySQL 8 (gocraft/dbr/v2) |
| Cache | Redis 7 |
| Auth | dmworkim internal API (octo-auth-client SDK) |

## Quick Start

```bash
# Run with Docker Compose
docker compose up -d

# Service available at http://localhost:8080
curl http://localhost:8080/health
```

## Development

```bash
# Prerequisites
# - Go 1.22+
# - MySQL 8
# - Redis 7

# Set environment variables
export MYSQL_DSN="root:root@tcp(localhost:3306)/octo_todo?charset=utf8mb4&parseTime=true"
export REDIS_URL="redis://localhost:6379/0"
export AUTH_URL="http://localhost:8090/internal/v1"
export SERVER_PORT=":8080"

# Run migrations
mysql -u root -p octo_todo < migrations/001_init.up.sql

# Build and run
go build -o todo-service ./cmd/main.go
./todo-service

# Run tests
go test ./...
```

## API Endpoints

All endpoints require `token` header for authentication and `X-Space-ID` header for Space context.

### Goals

| Method | Path | Description |
|--------|------|-------------|
| POST | /api/v1/goals | Create goal |
| GET | /api/v1/goals | List goals |
| GET | /api/v1/goals/:id | Get goal (kanban view) |
| PUT | /api/v1/goals/:id | Update goal |
| DELETE | /api/v1/goals/:id | Archive goal |
| POST | /api/v1/goals/:id/members | Add member |
| DELETE | /api/v1/goals/:id/members/:uid | Remove member |

### Todos

| Method | Path | Description |
|--------|------|-------------|
| POST | /api/v1/todos | Create todo |
| GET | /api/v1/todos | List todos (paginated, filterable) |
| GET | /api/v1/todos/:id | Get todo detail |
| PUT | /api/v1/todos/:id | Update todo |
| PUT | /api/v1/todos/:id/status | Transition status |
| DELETE | /api/v1/todos/:id | Soft delete |
| POST | /api/v1/todos/:id/assignees | Add assignee |
| DELETE | /api/v1/todos/:id/assignees/:uid | Remove assignee |
| PUT | /api/v1/todos/:id/assignee-status | Update own completion |

### Comments & Attachments

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/todos/:id/comments | List comments |
| POST | /api/v1/todos/:id/comments | Add comment |
| DELETE | /api/v1/todos/:id/comments/:cid | Delete comment |
| POST | /api/v1/todos/:id/attachments | Add attachment |
| DELETE | /api/v1/todos/:id/attachments/:aid | Delete attachment |

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | /health | Liveness check |
| GET | /health/ready | Readiness check |

## Design Document

See [docs/DESIGN.md](docs/DESIGN.md) for the full v4 architecture design.

## License

Proprietary — Octo / dmwork
