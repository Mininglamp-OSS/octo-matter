# Octo Todo - todo-service

Simple task microservice with open/closed status model. Full context: README.md, docs/DESIGN.md.

## Tech Stack
- Go 1.25+, Gin, gocraft/dbr/v2 (MySQL — NOT GORM)
- MySQL 8, Redis 7 (declared but not yet wired), google/uuid, go-playground/validator/v10

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
- `internal/service/`    business logic, permissions
- `internal/repository/` dbr queries (parameterized; no raw SQL concat)
- `internal/model/`      domain structs (todo.go, goal.go, assignee.go, etc.)
- `internal/auth/`       middleware (stub/jwt/bot modes)
- `internal/config/`     env-based config

## Key Invariants (gotchas)
- **Space scoping**: every query MUST filter by `space_id` from `X-Space-ID` header. Missing it = cross-tenant leak.
- **Todo status**: `open` or `closed`. No state machine. Creator or assignee can close/reopen. SetStatus is idempotent.
- **Permissions**: creator can do all edits + delete; assignees can close/reopen and update their own completion status.
- **Auth modes** — `AUTH_MODE=stub` parses `uid@name@role` tokens (dev only, logs WARN). `AUTH_MODE=jwt` validates RS256 JWTs via JWKS. `AUTH_MODE=bot` validates via POST /v1/auth/verify-bot. `APP_ENV=prod` rejects `AUTH_MODE=stub`.
- **UUIDs** generated in app (google/uuid), not DB.
- **dbr usage**: use `sess.Select(...).Where(...)` builders; never string-concat user input. See repository/*.go for patterns.

## Rules
- All code, comments, and commit messages in English
- Run `go build ./...` and `go test ./...` before every commit
- No GORM — dbr only
- Do not bypass the service layer from handlers
