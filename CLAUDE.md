# Octo Todo - todo-service

Goal-driven kanban task microservice. Full context: README.md, docs/DESIGN.md.

## Tech Stack
- Go 1.22+, Gin, gocraft/dbr/v2 (MySQL — NOT GORM)
- MySQL 8, Redis 7, google/uuid, go-playground/validator/v10

## Commands
```bash
go build ./...                 # build
go test ./...                  # all tests
go test ./internal/model -run TestX -v   # single test
docker compose up -d           # MySQL + Redis + service
mysql ... < migrations/001_init.up.sql   # apply migrations
```

## Architecture
`cmd/main.go` wires: config -> db -> repos -> services -> handlers -> Gin server.
Layers (strict direction, no skipping):
- `internal/handler/`    HTTP (Gin), request binding, resp helpers
- `internal/service/`    business logic, permissions, state machine
- `internal/repository/` dbr queries (parameterized; no raw SQL concat)
- `internal/model/`      domain structs + state machine (todo.go)
- `internal/auth/`       middleware (currently stubbed)
- `internal/config/`     env-based config

## Key Invariants (gotchas)
- **Space scoping**: every query MUST filter by `space_id` from `X-Space-ID` header. Missing it = cross-tenant leak.
- **Todo state machine** (model/todo.go `ValidTransitions`): `draft->planned->in_progress->done|cancelled`, plus `cancelled->draft`. Use `CanTransition` in services; never mutate status directly in repos.
- **Permissions**: creator can do all transitions + edits; assignees only `planned->in_progress` and `in_progress->done`. Enforce in service layer.
- **Auth is stubbed** — `internal/auth/middleware.go` parses `uid@name@role` tokens; do not replace without coordination (octo-auth-client SDK is the planned path).
- **UUIDs** generated in app (google/uuid), not DB.
- **dbr usage**: use `sess.Select(...).Where(...)` builders; never string-concat user input. See repository/*.go for patterns.

## Rules
- All code, comments, and commit messages in English
- Run `go build ./...` and `go test ./...` before every commit
- No GORM — dbr only
- Do not bypass the service layer from handlers
