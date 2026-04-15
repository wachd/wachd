# Wachd

**Self-hosted alert intelligence platform with root cause analysis.**

Wachd receives alerts from your monitoring tools (Grafana, Datadog, Prometheus) and automatically collects context, sanitizes PII, correlates events, and routes a plain-English diagnosis to the on-call engineer.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.24%2B-00ADD8.svg)](go.mod)
[![Go Report Card](https://goreportcard.com/badge/github.com/wachd/wachd)](https://goreportcard.com/report/github.com/wachd/wachd)

---

## How It Works

When an alert fires, Wachd:

1. Receives the webhook from Grafana, Datadog, Splunk, or any compatible source
2. Fetches context — recent commits, error logs, metric history around the alert time
3. Strips all PII from the collected context before any analysis
4. Runs root cause correlation to build a causal timeline
5. Looks up who is on-call for that team
6. Sends a notification with the probable cause and suggested action

No agents run in your application clusters. Wachd receives alerts inbound via HTTPS and calls your monitoring APIs outbound. Your data stays in your infrastructure.

---

## Architecture

```
Your Monitoring (Grafana / Datadog / Prometheus)
    │
    └──► POST /api/v1/webhook/{teamId}/{secret}
              │
              ▼
         Redis Queue
              │
              ▼
         Worker Process
           ├── Collect context (GitHub, Loki, Prometheus)
           ├── Sanitize PII
           ├── Correlate events → causal timeline
           ├── Run root cause analysis (Ollama)
           └── Notify on-call engineer (Slack + Email)
```

See [`docs/`](docs/) for architecture diagrams.

---

## Authentication

Wachd includes an enterprise authentication system with local users, SSO (OIDC), and API tokens. On the first startup a superadmin account is created automatically and the credentials are printed to the server log.

See [docs/authentication.md](docs/authentication.md) for the complete guide, including:

- Bootstrap admin setup
- Local user management
- Password policy configuration
- Local groups and team access
- SSO provider configuration (Microsoft Entra, Okta, Google Workspace)
- Group mappings (AD group to Wachd team)
- Kubernetes secrets setup

See [docs/api-tokens.md](docs/api-tokens.md) for programmatic API access using Bearer tokens.

---

## Prerequisites

- Go 1.24+
- Docker and Docker Compose
- Make

---

## Quick Start

### 1. Start infrastructure

```bash
make docker-up
```

Starts PostgreSQL and Redis in Docker.

### 2. Install dependencies

```bash
make deps
```

### 3. Configure environment

Copy the example environment file and fill in your values:

```bash
cp .env.example .env
```

Key settings in `.env`:

```bash
DATABASE_URL=postgres://wachd:wachd@localhost:5432/wachd
REDIS_URL=redis://localhost:6379

# Analysis backend (Ollama for local, air-gapped deployments)
AI_BACKEND=ollama
OLLAMA_ENDPOINT=http://localhost:11434
OLLAMA_MODEL=llama3.2

# Optional — notifications
SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=alerts@example.com
SMTP_PASS=your-smtp-password
```

### 4. Start the server, worker, and web dashboard

```bash
make dev
```

Opens:
- API server on `http://localhost:8080`
- Web dashboard on `http://localhost:3000`

### 5. Send a test alert

```bash
make test-webhook
```

---

## Project Structure

```
wachd/
├── cmd/
│   ├── server/          # Webhook receiver and REST API
│   └── worker/          # Background job processor
├── internal/
│   ├── store/           # PostgreSQL models and queries
│   ├── queue/           # Redis job queue
│   ├── collector/       # Context collection (GitHub, Loki, Prometheus)
│   ├── sanitiser/       # PII removal — 21 regex patterns
│   ├── correlator/      # Event correlation and causal timeline
│   ├── oncall/          # On-call schedule and rotation engine
│   ├── notify/          # Slack and email notifications
│   └── ai/              # Pluggable analysis backend (Ollama)
├── web/                 # Next.js 15 dashboard (TypeScript, Tailwind)
├── helm/wachd/          # Helm chart for Kubernetes deployment
├── scripts/             # Development and review utilities
└── docs/                # Architecture diagrams
```

---

## Makefile Commands

```bash
make help          # Show all available commands
make dev           # Run server + worker + web dashboard
make server        # Run API server only
make worker        # Run background worker only
make web           # Run web dashboard only
make test          # Run all tests
make build         # Build server and worker binaries
make test-webhook  # Send a test alert to the local server
make logs          # Follow Docker Compose logs
make clean         # Remove build artifacts
```

---

## API Reference

### Receive an alert

```
POST /api/v1/webhook/{teamId}/{secret}
```

Accepts JSON payloads from Grafana, Datadog, Prometheus Alertmanager, or any custom source.

### Incidents

```
GET  /api/v1/teams/{teamId}/incidents
GET  /api/v1/teams/{teamId}/incidents/{incidentId}
POST /api/v1/teams/{teamId}/incidents/{incidentId}/ack
POST /api/v1/teams/{teamId}/incidents/{incidentId}/resolve
POST /api/v1/teams/{teamId}/incidents/{incidentId}/snooze
```

### On-call

```
GET /api/v1/teams/{teamId}/oncall/now
GET /api/v1/teams/{teamId}/schedule
PUT /api/v1/teams/{teamId}/schedule
```

### Utility

```
GET /api/v1/health    # Liveness probe
GET /api/v1/metrics   # Prometheus metrics endpoint
```

---

## Kubernetes Deployment

Wachd ships with a production-grade Helm chart.

### Prerequisites

- PostgreSQL 15+ (AWS RDS, Azure DB for PostgreSQL, GCP Cloud SQL, or self-hosted)
- Redis 7+ (AWS ElastiCache, Azure Cache for Redis, or self-hosted)
- Kubernetes 1.26+
- Helm 3.10+
- cert-manager v1.13+ installed in the cluster

**Optional prerequisites** (required only if you enable the corresponding features):

- **nginx ingress controller** — required if `ingress.enabled: true`
  ```bash
  helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
  helm repo update
  helm install ingress-nginx ingress-nginx/ingress-nginx --namespace ingress-nginx --create-namespace
  ```
- **metrics-server** — required if `server.hpa.enabled: true` or `worker.hpa.enabled: true`
  ```bash
  helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/
  helm repo update
  helm install metrics-server metrics-server/metrics-server --namespace kube-system
  ```

The default chart values have `ingress.enabled: false` and `hpa.enabled: false`, so neither is needed for a basic install.

**Install cert-manager (once per cluster):**

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true

# Wait for cert-manager to be fully ready before proceeding
kubectl rollout status deployment/cert-manager -n cert-manager
kubectl rollout status deployment/cert-manager-webhook -n cert-manager
```

cert-manager is a cluster-level component — install it once and all applications share it.

### Install

```bash
# Create namespace
kubectl create namespace wachd

# Create secrets for database and Redis credentials
kubectl create secret generic wachd-db-secret \
  --from-literal=password=<your-db-password> \
  -n wachd

kubectl create secret generic wachd-redis-secret \
  --from-literal=password=<your-redis-password> \
  -n wachd

# Install the chart
helm install wachd ./helm/wachd \
  --namespace wachd \
  --set postgres.external.host=<your-db-host> \
  --set postgres.external.username=<your-db-user> \
  --set postgres.external.database=wachd \
  --set redis.external.host=<your-redis-host>
```

The server **automatically creates all database tables on first startup** — no manual schema setup required. On the very first run it also prints your team ID and webhook secret to the logs:

```
╔══════════════════════════════════════════════════════╗
║              WACHD — FIRST RUN SETUP                ║
╠══════════════════════════════════════════════════════╣
║  Team ID:       <uuid>                               ║
║  Webhook secret: <secret>                            ║
╠══════════════════════════════════════════════════════╣
║  Send alerts to:                                     ║
║  POST /api/v1/webhook/<uuid>/                        ║
╚══════════════════════════════════════════════════════╝
```

Paste that webhook URL into Grafana / Datadog alert routing and you are live.

> **Note — TLS certificate on first install:** cert-manager issues the internal mTLS certificate after the chart is deployed. Pods will be `Running` within about 30 seconds, but if you see `CrashLoopBackOff` or failed readiness probes in the first 1–2 minutes, wait for the certificate to be issued:
> ```bash
> kubectl get certificate -n wachd   # STATUS should reach True
> ```

> **Redis without authentication:** If your Redis instance has no password, skip creating `wachd-redis-secret` and add `--set redis.external.passwordSecret=""` to your install command.

### Configuration reference

See [`helm/wachd/values.yaml`](helm/wachd/values.yaml) for all available options.

The chart includes:
- Horizontal Pod Autoscaler (server: 2–10 replicas, worker: 1–5 replicas)
- PodDisruptionBudget for zero-downtime upgrades
- Pod anti-affinity across nodes and zones
- Non-root containers with read-only root filesystem
- Two-tier TLS via cert-manager (internal mTLS + external ingress)
- External PostgreSQL and Redis support (AWS RDS, Azure DB, ElastiCache)

---

## PII Sanitizer

Before any data is sent to the analysis backend, the sanitizer strips:

| Pattern | Replacement |
|---|---|
| Email addresses | `[EMAIL]` |
| IPv4 / IPv6 addresses | `[IP]` |
| UUIDs and numeric account IDs | `[ID]` |
| Credit card patterns | `[CARD]` |
| API keys and tokens (heuristic) | `[SECRET]` |
| Internal hostnames / FQDNs | `[HOST]` |
| JWT tokens | `[TOKEN]` |
| AWS / GCP / Azure resource ARNs | `[RESOURCE]` |

What is preserved for analysis: error type, stack trace structure, service name, timestamp, metric values, commit hash, file path, function name, HTTP status codes.

---

## Open Source Limits

The open-source tier is suitable for evaluation and small teams. Limits enforced in code:

- 1 team
- 5 users
- 1,000 alerts per month
- Local analysis backend only (Ollama)

---

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on reporting bugs, proposing features, and submitting pull requests.

---

## Security

To report a security vulnerability, see [SECURITY.md](SECURITY.md). Please do not open a public issue for security concerns.

---

## License

Apache 2.0. See [LICENSE](LICENSE) for the full text.

Copyright 2025 NTC Dev
