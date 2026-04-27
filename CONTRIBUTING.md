# Contributing to Octo Todo

Thank you for your interest in contributing! This guide will help you get started.

## Development Environment

### Prerequisites

- **Go 1.25+** — [download](https://go.dev/dl/)
- **MySQL 8.0** — for integration testing / local dev
- **Docker & Docker Compose** — for running the full stack locally

### Setup

```bash
git clone https://github.com/Mininglamp-OSS/octo-matter.git
cd todos
cp .env.example .env   # adjust DB credentials if needed
docker compose up -d   # starts MySQL + Redis
go build ./...
go test ./...
```

## Code Style

- Run `gofmt` on all Go files before committing.
- Use [`golangci-lint`](https://golangci-lint.run/) for static analysis:
  ```bash
  golangci-lint run ./...
  ```
- Follow standard Go conventions: effective Go, Go Code Review Comments.
- All code, comments, and commit messages must be in **English**.

## Commit Convention

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>
```

**Types:** `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `ci`

Examples:
- `feat(handler): add list todos endpoint`
- `fix(repository): correct space_id filter in query`
- `docs: update README setup instructions`

## Pull Request Process

1. **Branch off `main`** — use a descriptive branch name (e.g. `feat/add-labels`, `fix/cursor-pagination`).
2. **Keep PRs focused** — one logical change per PR.
3. **Ensure CI passes** — `go vet`, `go build`, and `go test` must all succeed.
4. **Write tests** — new features require tests; bug fixes should include a regression test.
5. **Update documentation** — if your change affects the API or configuration, update README.md or docs/.
6. **Request review** — at least one approval is required before merging.

## Testing

- All tests must pass before submitting a PR:
  ```bash
  go test ./...
  ```
- Run a specific test with:
  ```bash
  go test ./internal/model -run TestIsValidStatus -v
  ```
- Use table-driven tests where appropriate.
- Use fakes over mocks for repository interfaces.

## Project Structure

```
cmd/main.go              # entry point
internal/
  handler/               # HTTP layer (Gin)
  service/               # business logic
  repository/            # database queries (dbr)
  model/                 # domain structs
  auth/                  # authentication middleware
  config/                # environment-based config
migrations/              # SQL migration files
```

## Reporting Issues

Open an issue on GitHub with:
- A clear title and description
- Steps to reproduce (if applicable)
- Expected vs actual behavior
- Go version and OS

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
