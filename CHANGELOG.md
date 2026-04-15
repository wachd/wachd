# Changelog

All notable changes to Wachd will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
Wachd uses [Semantic Versioning](https://semver.org/).

---

## [Unreleased]

### Added

**Enterprise authentication system**

- Bootstrap admin — on first startup with an empty `local_users` table, a `wachd_admin` superadmin is created and the generated password is printed once to stdout. The account is flagged `force_password_change: true` and requires an immediate password change before any other action is permitted.

- Local users — superadmin-managed auth identities independent of SSO. Endpoints: `GET/POST /api/v1/admin/users`, `GET/PUT/DELETE /api/v1/admin/users/{id}`, `POST /api/v1/admin/users/{id}/reset-password`.

- Local groups — collections of local users granted team access with a role (`viewer`, `responder`, `admin`). Group membership resolves team access on every login. Endpoints: full CRUD under `/api/v1/admin/groups/{id}/members` and `/api/v1/admin/groups/{id}/access`.

- DB-stored SSO providers — OIDC provider configuration (issuer URL, client ID, AES-256-GCM encrypted client secret, scopes) is stored in the database and loaded via a 60-second in-memory cache. Providers can be updated without restarting the server. Endpoints: full CRUD under `/api/v1/admin/sso/providers`, plus `POST /api/v1/admin/sso/providers/{id}/test` for OIDC discovery verification.

- Group mappings — map SSO directory groups (by object ID or claim value) to Wachd teams and roles. Managed per team through Admin → Teams → Manage. Endpoints: `GET/POST /api/v1/admin/group-mappings`, `DELETE /api/v1/admin/group-mappings/{id}`.

- Password policy — singleton configuration for minimum length, character class requirements, maximum failed attempts, and lockout duration. Enforced at login, password change, user creation, and password reset. Endpoints: `GET/PUT /api/v1/admin/password-policy`.

- `POST /auth/local/login` — local username and password authentication. Returns `force_password_change` flag; the frontend redirects to `/change-password` when set.

- `POST /auth/local/change-password` — change password for the authenticated local user. Validates against the active password policy and refreshes the Redis session to clear `force_password_change` without requiring re-login.

- `RequireSuperAdmin` middleware — gates all `/api/v1/admin/*` routes to superadmin accounts only.

- `RequireNoForceChange` middleware — blocks all protected routes until the user completes a mandatory password change.

- Legacy Entra migration — on first startup with `WACHD_ENCRYPTION_KEY` set, if `ENTRA_TENANT_ID`/`ENTRA_CLIENT_ID`/`ENTRA_CLIENT_SECRET` env vars are present and the `sso_providers` table is empty, the values are migrated to a database row automatically.

**API tokens (personal access tokens)**

- `wachd_`-prefixed tokens generated with 32 bytes from `crypto/rand` (256 bits of entropy). Only the SHA-256 hash is stored; the raw token is returned once on creation.

- `BearerOrCookie` authentication middleware replaces `sessions.RequireAuth` on all protected routes. Checks `Authorization: Bearer <token>` first, resolves team access fresh from the database, then falls back to cookie-based sessions.

- `crypto/subtle.ConstantTimeCompare` applied to token hash verification in the middleware layer as defence-in-depth against timing side channels.

- Token-authenticated requests bypass `force_password_change` enforcement so CI pipelines are never interrupted by interactive password rotation requirements.

- Deactivating a user immediately invalidates their tokens (enforced by `AND u.is_active = true` in the lookup query).

- Endpoints: `GET/POST /api/v1/admin/tokens`, `DELETE /api/v1/admin/tokens/{id}`.

**Admin panel (web)**

- `/admin` — superadmin-only section, hidden from regular users via session gate in layout.
- `/admin/users` — list, create (auto-generated password shown once with reveal/copy), deactivate, delete local users.
- `/admin/groups` — list, create, delete groups; `/admin/groups/{id}` manages members and team access in tabbed view.
- `/admin/teams` — list and create teams; `/admin/teams/{id}` manages group mappings (AD group → team role).
- `/admin/sso` — list and create SSO providers; `/admin/sso/{id}` edits provider config and runs a connection test.
- `/admin/password-policy` — view and update the active password policy.
- `/admin/tokens` — generate and revoke personal access tokens; generated token shown once with reveal/copy/dismiss banner.

**Documentation**

- `docs/authentication.md` — full enterprise auth guide covering bootstrap admin, local users, password policy, local groups, SSO providers, group mappings, and Kubernetes setup.
- `docs/api-tokens.md` — API token guide covering creation, usage, security model, and CI/CD examples (GitHub Actions, GitLab CI, Terraform, shell script).

### Changed

- `sessions.RequireAuth` (cookie-only) replaced by `auth.BearerOrCookie` on all protected routes — existing browser sessions are unaffected.
- `GET /auth/me` response extended with `auth_type`, `is_superadmin`, and `force_password_change` fields.
- Admin panel navigation adds **Teams**, **API Tokens**, and conditionally shows the **Admin** link in the top navigation bar for superadmin accounts.

### Planned

- Open-source tier limit enforcement (1 team, 5 users, 1,000 alerts/month)
- GitHub Actions CI — build and publish Docker image to GHCR on push to `main`
- `v0.1.0` release tag

---

## [0.1.0] — Unreleased (Phase 1 MVP)

### Added

**Core infrastructure**
- Go project structure (`cmd/server`, `cmd/worker`, `internal/`)
- PostgreSQL schema — teams, incidents, users, schedules
- Redis job queue with worker process
- Webhook receiver: `POST /api/v1/webhook/{teamId}/{secret}`
- Health check endpoint: `GET /api/v1/health`
- Docker Compose for local development
- Makefile with `make dev`, `make test`, `make build`

**Notifications and on-call routing**
- On-call schedule engine with weekly rotation (`internal/oncall/schedule.go`)
- Slack webhook notifications (`internal/notify/slack.go`)
- SMTP email notifications (`internal/notify/email.go`)
- Worker flow: receive alert → look up on-call → send notifications

**Context collection and analysis**
- GitHub API client — fetch recent commits (`internal/collector/git.go`)
- Loki API client — fetch service logs (`internal/collector/logs.go`)
- Prometheus API client — fetch metric history (`internal/collector/metrics.go`)
- PII sanitizer with 21 regex patterns, 20 unit tests (`internal/sanitiser/sanitiser.go`)
- Correlator — builds causal timeline from collected context (`internal/correlator/correlator.go`)
- Pluggable analysis backend interface (`internal/ai/interface.go`)
- Ollama backend for local, air-gapped deployments (`internal/ai/ollama.go`)
- Fail-safe: alert is routed without analysis if backend is unavailable
- Analysis results stored in `incidents.analysis` JSONB field

**Incident lifecycle**
- Acknowledge, resolve, reopen, and snooze endpoints
- Status history tracking

**Web dashboard**
- Next.js 15 + TypeScript + Tailwind CSS (`web/`)
- Incidents list — severity and status badges, analysis preview
- Incident detail — alert payload, root cause, context (commits, logs, metrics)
- Team settings — webhook URL, notification config, data sources
- On-call schedule — weekly calendar view
- Responsive layout (mobile, tablet, desktop)

**Kubernetes**
- Production Helm chart (`helm/wachd/`)
- Two-tier TLS via cert-manager (internal mTLS + external ingress)
- External PostgreSQL support (AWS RDS, Azure DB, GCP Cloud SQL)
- External Redis support (ElastiCache, Azure Cache, TLS + Sentinel)
- Horizontal Pod Autoscaler — server (2–10), worker (1–5)
- PodDisruptionBudget for zero-downtime upgrades
- Pod anti-affinity across nodes and zones
- Non-root containers, read-only root filesystem, dropped capabilities
- Resource requests and limits

---

[Unreleased]: https://github.com/wachd/wachd/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/wachd/wachd/releases/tag/v0.1.0

### Added

**Core infrastructure**
- Go project structure (`cmd/server`, `cmd/worker`, `internal/`)
- PostgreSQL schema — teams, incidents, users, schedules
- Redis job queue with worker process
- Webhook receiver: `POST /api/v1/webhook/{teamId}/{secret}`
- Health check endpoint: `GET /api/v1/health`
- Docker Compose for local development
- Makefile with `make dev`, `make test`, `make build`

**Notifications and on-call routing**
- On-call schedule engine with weekly rotation (`internal/oncall/schedule.go`)
- Slack webhook notifications (`internal/notify/slack.go`)
- SMTP email notifications (`internal/notify/email.go`)
- Worker flow: receive alert → look up on-call → send notifications

**Context collection and analysis**
- GitHub API client — fetch recent commits (`internal/collector/git.go`)
- Loki API client — fetch service logs (`internal/collector/logs.go`)
- Prometheus API client — fetch metric history (`internal/collector/metrics.go`)
- PII sanitizer with 21 regex patterns, 20 unit tests (`internal/sanitiser/sanitiser.go`)
- Correlator — builds causal timeline from collected context (`internal/correlator/correlator.go`)
- Pluggable analysis backend interface (`internal/ai/interface.go`)
- Ollama backend for local, air-gapped deployments (`internal/ai/ollama.go`)
- Fail-safe: alert is routed without analysis if backend is unavailable
- Analysis results stored in `incidents.analysis` JSONB field

**Incident lifecycle**
- Acknowledge, resolve, reopen, and snooze endpoints
- Status history tracking

**Web dashboard**
- Next.js 15 + TypeScript + Tailwind CSS (`web/`)
- Incidents list — severity and status badges, analysis preview
- Incident detail — alert payload, root cause, context (commits, logs, metrics)
- Team settings — webhook URL, notification config, data sources
- On-call schedule — weekly calendar view
- Responsive layout (mobile, tablet, desktop)

**Kubernetes**
- Production Helm chart (`helm/wachd/`)
- Two-tier TLS via cert-manager (internal mTLS + external ingress)
- External PostgreSQL support (AWS RDS, Azure DB, GCP Cloud SQL)
- External Redis support (ElastiCache, Azure Cache, TLS + Sentinel)
- Horizontal Pod Autoscaler — server (2–10), worker (1–5)
- PodDisruptionBudget for zero-downtime upgrades
- Pod anti-affinity across nodes and zones
- Non-root containers, read-only root filesystem, dropped capabilities
- Resource requests and limits

---

[Unreleased]: https://github.com/wachd/wachd/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/wachd/wachd/releases/tag/v0.1.0
