# Milestones

## Phase 1 — Open Source MVP

### Month 1: Foundation

**Week 1-2: Core Infrastructure** ✅
- Go project structure (`cmd/server`, `cmd/worker`, `internal/`)
- PostgreSQL schema (teams, incidents, users, schedules)
- Redis job queue
- Webhook receiver: `POST /api/v1/webhook/{teamId}/{secret}`
- Worker process consuming from Redis
- Health check: `/api/v1/health`
- Docker Compose for local development
- Makefile (`make dev`, `make test`, `make build`)

**Week 3-4: Notifications + On-Call Routing** ✅
- On-call schedule engine (`internal/oncall/schedule.go`)
  - Weekly rotation — one person per week
  - `GetCurrentOnCall(teamID) → User`
- Slack webhook notifications (`internal/notify/slack.go`)
- SMTP email notifications (`internal/notify/email.go`)
- Worker flow: receive alert → look up on-call → send notifications

---

### Month 2: Analysis Engine

**Week 5-6: Context Collection + PII Sanitizer** ✅
- GitHub API client — fetch recent commits (`internal/collector/git.go`)
- Loki API client — fetch service logs (`internal/collector/logs.go`)
- Prometheus API client — fetch metric history (`internal/collector/metrics.go`)
- PII sanitizer with 21 regex patterns (`internal/sanitiser/sanitiser.go`)
  - Strips: email, IP, UUID, JWT, API keys, credit cards, phone, SSN, tokens
  - 20 unit tests — all passing
- Correlator — builds causal timeline from collected context (`internal/correlator/correlator.go`)
- Worker updated to collect + sanitize context before analysis

**Week 7-8: Analysis Backend** ✅
- Pluggable backend interface (`internal/ai/interface.go`)
- Ollama backend for local, air-gapped deployments (`internal/ai/ollama.go`)
- Structured prompt template for SRE root cause analysis
- Fail-safe: if backend unavailable, alert is still routed without analysis
- Analysis results stored in `incidents.analysis` JSONB field
- Slack and email notifications include analysis when available

---

### Month 3: Web Dashboard + Helm Chart

**Week 9-10: Web Dashboard** ✅
- Next.js 15 + TypeScript + Tailwind CSS (`web/`)
- Incidents list — severity/status badges, analysis preview
- Incident detail — alert payload, root cause, context (commits, logs, metrics)
- Team settings — webhook URL, notification config, data sources
- On-call schedule — weekly calendar view
- Responsive design (mobile/tablet/desktop)

**Week 11-12: Production Helm Chart** ✅
- Full Helm chart with enterprise-grade defaults (`helm/wachd/`)
- Two-tier TLS strategy via cert-manager:
  - Internal mTLS — self-signed CA, 3-year validity, auto-provisioned
  - External ingress — bring-your-own cert, Let's Encrypt, or self-signed
- External PostgreSQL support (AWS RDS, Azure DB, GCP Cloud SQL)
- External Redis support (ElastiCache, Azure Cache, with TLS + Sentinel)
- HPA for server (min 2, max 10) and worker (min 1, max 5)
- PodDisruptionBudget for zero-downtime upgrades
- Pod anti-affinity to spread across nodes/zones
- Non-root containers, read-only root filesystem, dropped capabilities
- Resource requests + limits for Burstable QoS class

---

## Current Status

All Phase 1 milestones complete. Preparing for Phase 1 public launch.

**Remaining before launch:**
- [ ] Open-source tier limits enforced in code (1 team, 5 users, 1000 alerts/month)
- [ ] GitHub Actions CI — build and publish Docker image to GHCR on push to `main`
- [ ] `README.md` — quickstart, architecture, connector setup
- [ ] `v0.1.0` release tag

---

## Phase 2 — Planned

- Multi-team support (license enforcement)
- Cloud analysis backends (unlocked with license)
- Advanced on-call scheduling (overrides, escalation chains)
- CVE feed matching against deployed dependencies
- Auto-PR creation for security fixes
- Incident analytics dashboard (MTTD/MTTR trends)
- Hosted option

---

## Phase 3 — Planned

- SSO/SAML (Okta, Azure AD, Google Workspace)
- Granular RBAC (viewer, responder, admin, security-analyst)
- Tamper-proof audit logs for compliance
- SOC2/ISO27001 evidence export
- Custom analysis model fine-tuning
- Multi-region active-active deployment
- Air-gapped deployment consulting
