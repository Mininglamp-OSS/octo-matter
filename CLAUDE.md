# Octo Todo - todo-service

Simple task microservice with open/closed status model + goal organization. Full context: README.md, docs/DESIGN.md.

## Tech Stack
- Go 1.25+, Gin, gocraft/dbr/v2 (MySQL — NOT GORM)
- MySQL 8, google/uuid, go-playground/validator/v10

## Commands
```bash
go build ./...                 # build
go test ./...                  # all tests
go test ./internal/model -run TestX -v   # single test
docker compose up -d           # MySQL + service
mysql ... < migrations/001_init.up.sql   # apply migrations
```

## Architecture
`cmd/main.go` wires: config -> db -> repos -> services -> handlers -> Gin server.
Layers (strict direction, no skipping):
- `internal/handler/`       HTTP (Gin), request binding, resp helpers
- `internal/service/`       business logic, permissions
- `internal/repository/`    dbr queries (parameterized; no raw SQL concat)
- `internal/model/`         domain structs (todo.go, goal.go, assignee.go, etc.)
- `internal/auth/`          middleware — calls dmworkim verify API for auth + Space check
- `internal/notification/`  system notifications via dmworkim internal notify API (X-Internal-Token auth)
- `internal/config/`        env-based config

## Key Invariants (gotchas)
- **Space scoping**: every query MUST filter by `space_id` from `X-Space-ID` header. Missing it = cross-tenant leak.
- **Todo status**: `open` or `closed`. No state machine. Creator or assignee can close/reopen.
- **Goal status**: `active`, `completed`, or `archived`. Creator controls status.
- **Permissions**: creator can do all edits + delete; assignees can close/reopen.
- **Auth** — Calls dmworkim public API: `token` header → POST /v1/auth/verify, `Authorization: Bearer` → POST /v1/auth/verify-bot. Config: `DMWORKIM_URL`.
- **Bot-owner visibility**: users see their bots' todos, bots see their owner's todos. Implemented via `related_uids` (CallerUIDs IN ? queries).
- **Notifications**: sent via dmworkim POST /v1/internal/notify with X-Internal-Token. Config: `DMWORKIM_URL` (base URL), `NOTIFY_INTERNAL_TOKEN` (auth token).
- **UUIDs** generated in app (google/uuid), not DB.
- **dbr usage**: use `sess.Select(...).Where(...)` builders; never string-concat user input.

## Rules
- All code, comments, and commit messages in English
- Run `go build ./...` and `go test ./...` before every commit
- No GORM — dbr only
- Do not bypass the service layer from handlers
