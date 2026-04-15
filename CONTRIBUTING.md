# Contributing to Wachd

Thank you for your interest in contributing. This document covers how to report bugs, propose features, and submit pull requests.

## Table of Contents

- [Reporting Bugs](#reporting-bugs)
- [Requesting Features](#requesting-features)
- [Development Setup](#development-setup)
- [Submitting a Pull Request](#submitting-a-pull-request)
- [Code Standards](#code-standards)
- [Commit Messages](#commit-messages)

---

## Reporting Bugs

Open an issue using the [Bug Report](.github/ISSUE_TEMPLATE/bug_report.yml) template. Include:

- What you did
- What you expected to happen
- What actually happened
- Wachd version and deployment method (Docker Compose / Helm)
- Relevant logs (`make logs` or `kubectl logs`)

---

## Requesting Features

Open an issue using the [Feature Request](.github/ISSUE_TEMPLATE/feature_request.yml) template. Describe the problem you're trying to solve and how you'd expect the feature to work.

---

## Development Setup

### Requirements

- Go 1.24+
- Docker and Docker Compose
- Make
- Node.js 20+ (for web dashboard)

### Local environment

```bash
git clone https://github.com/wachd/wachd.git
cd wachd

# Start PostgreSQL and Redis
make docker-up

# Install Go and Node dependencies
make deps

# Copy and configure environment
cp .env.example .env
# Edit .env with your local settings

# Start everything
make dev
```

### Running tests

```bash
make test        # Go unit tests
go vet ./...     # Static analysis
```

---

## Submitting a Pull Request

1. Fork the repository and create a branch from `main`.
2. Write tests for any new behaviour.
3. Make sure all checks pass:
   ```bash
   make test
   go vet ./...
   go build ./...
   ```
4. Open a pull request against `main`. Fill out the pull request template.

### What gets reviewed

- **Correctness** — logic is sound, tests cover the change
- **Multi-tenancy isolation** — every database query that touches team data must filter by `team_id`
- **Security** — SQL uses parameterized queries; no secrets in code; input is validated; PII is sanitized before analysis
- **Error handling** — functions return errors, not panics

Pull requests that skip multi-tenancy checks or send unsanitized data to the analysis backend will not be merged.

---

## Code Standards

### Go

- Follow standard Go conventions (`gofmt`, `go vet` must pass)
- Return errors — do not `panic` in production paths
- Database queries must use `$1`, `$2` parameters (never string concatenation)
- All queries against tenant data must include `WHERE team_id = $N`
- Log with context: include incident ID, team name where relevant

### Adding a notification channel

Implement the `Notifier` interface in `internal/notify/` and register it in the worker.

### Adding an analysis backend

Implement the `Backend` interface in `internal/ai/` and add the backend name to the `AI_BACKEND` env var switch.

### Adding an alert source

Implement the `WebhookParser` interface in `internal/collector/` and add the parser to the server's webhook handler.

---

## Commit Messages

Use the format: `type: short description`

Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`

Examples:
- `feat: add Microsoft Teams notification channel`
- `fix: handle empty on-call schedule gracefully`
- `docs: update Helm chart configuration reference`

---

## License

By contributing, you agree that your contributions will be licensed under the Apache 2.0 License.
