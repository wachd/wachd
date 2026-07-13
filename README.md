# Wachd

**Self-hosted alert intelligence platform with root cause analysis.**

Wachd receives alerts from your monitoring tools (Grafana, Datadog, Prometheus) and automatically collects context, sanitizes PII, correlates events, and routes a plain-English diagnosis to the on-call engineer.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.24%2B-00ADD8.svg)](go.mod)
[![Go Report Card](https://goreportcard.com/badge/github.com/wachd/wachd)](https://goreportcard.com/report/github.com/wachd/wachd)
[![Discord](https://img.shields.io/badge/Discord-join-5865F2?logo=discord&logoColor=white)](https://discord.gg/zV86KNgkcJ)
[![GitHub Stars](https://img.shields.io/github/stars/wachd/wachd?style=social)](https://github.com/wachd/wachd)

If Wachd is useful to you, a ⭐ on GitHub helps other engineers find it.

![Wachd demo — on-call check, alert fire, AI root cause analysis](docs/demo.gif)

> **Founders Program — 10 lifetime SMB licenses**
> We're giving 10 engineering teams a lifetime Wachd SMB license at no cost. Deploy it, use it seriously, tell us what's broken. [Claim a slot on Discord](https://discord.gg/zV86KNgkcJ) or email sales@wachd.io.

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

# Trusted ingress/proxy CIDRs for webhook client IP extraction.
# Empty means X-Forwarded-For is ignored and rate limiting uses RemoteAddr.
TRUSTED_PROXY_CIDRS=

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

## Deploy with Docker Compose

The fastest way to self-host Wachd on a single server or VPS. No Kubernetes required.

```bash
git clone https://github.com/wachd/wachd
cd wachd
make compose-up
```

> **If you have a local `.env` from development:** `DATABASE_URL` and `REDIS_URL` use `localhost` hostnames, which won't resolve inside Docker. Either remove the `.env` file (the compose defaults use Docker service names) or update those two values to `postgres:5432` and `redis:6379`.

**First run — get your admin credentials:**

On the very first startup, Wachd creates a superadmin account and prints the credentials once to the server log. Retrieve them with:

```bash
docker compose logs wachd-server | grep -A 6 "BOOTSTRAP ADMIN"
```

You will see:

```
║  Username: wachd_admin                        ║
║  Password: <generated-password>               ║
```

Log in at **http://localhost:3000** and change the password immediately when prompted.

> **Before going to production:** generate a unique encryption key and set it in `.env`:
> ```bash
> cp .env.example .env
> echo "WACHD_ENCRYPTION_KEY=$(openssl rand -hex 32)" >> .env
> ```
> The default key in `docker-compose.yml` is public — anyone using it can decrypt credentials stored in your database. Fine for local evaluation, not for production.

By default Wachd uses Ollama for AI analysis (no external API calls). Pull a model after the stack starts:

```bash
docker exec wachd-ollama ollama pull llama3.2
```

**To use OpenAI instead**, copy `.env.example` to `.env` and set:

```bash
AI_BACKEND=openai
OPENAI_API_KEY=sk-...
```

Then `make compose-up` again.

**To stop:**

```bash
make compose-down
```

> **Why not `docker compose down` directly?** The app services (`wachd-server`, `wachd-worker`, `wachd-web`) use the `app` profile. Running `docker compose down` without `--profile app` skips them, leaving the network in use. Always use `make compose-down` — or `docker compose --profile app down` if you prefer the raw command.

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
║  POST /api/v1/webhook/<uuid>/<secret>                ║
╚══════════════════════════════════════════════════════╝
```

Paste that webhook URL into Grafana / Datadog alert routing and you are live.

### Trusted proxy CIDRs

Wachd rate-limits webhook requests by client IP.

By default, `TRUSTED_PROXY_CIDRS` is empty. This is the safe default: Wachd ignores `X-Forwarded-For` and rate-limits by the direct `RemoteAddr`.

If Wachd is deployed behind an ingress controller or load balancer and you want webhook rate limiting to use the original client IP, configure trusted proxy CIDRs.

For local or direct environment configuration:

```bash
TRUSTED_PROXY_CIDRS=10.0.0.0/8,172.16.0.0/12
```

For Helm deployments:

```yaml
ingress:
  trustedProxyCIDRs: "10.0.0.0/8,172.16.0.0/12"
```

Only set this to CIDRs for proxies or load balancers you actually control. Wachd only trusts `X-Forwarded-For` when the immediate peer is in this list.

Your ingress should also overwrite or normalize `X-Forwarded-For` rather than append attacker-controlled values. For nginx-ingress, check settings such as:

```yaml
use-forwarded-headers: "false"
compute-full-forwarded-for: "false"
```

**Behavior change:** after the trusted-proxy hardening, existing deployments behind an ingress with the default empty value will rate-limit per ingress/proxy IP rather than per original client IP until `TRUSTED_PROXY_CIDRS` or `ingress.trustedProxyCIDRs` is configured.

> **Note — TLS certificate on first install:** cert-manager issues the internal mTLS certificate after the chart is deployed. Pods will be `Running` within about 30 seconds, but if you see `CrashLoopBackOff` or failed readiness probes in the first 1–2 minutes, wait for the certificate to be issued:
> ```bash
> kubectl get certificate -n wachd   # STATUS should reach True
> ```

> **Redis without authentication:** If your Redis instance has no password, skip creating `wachd-redis-secret` and add `--set redis.external.passwordSecret=""` to your install command.

### First login — superadmin setup

After deployment, log in with the bootstrap superadmin credentials printed in the server log. **Everything below is done via the GUI or API — no redeployment needed.**

#### AI backend

The AI backend is a **platform-wide setting** configured by the superadmin — not a per-team choice. The enterprise decides whether to run Ollama (air-gapped), Claude, or OpenAI for the whole platform. Set the initial backend in `values.yaml` before deployment:

```yaml
analysis:
  backend: claude    # ollama | claude | openai | gemini
```

> GUI/API configuration of the AI backend (without redeploying) is tracked in [wachd/wachd#1](https://github.com/wachd/wachd/issues/1).

#### SSO / identity providers

Configure one or more OIDC providers so team members sign in with their existing directory:

```
POST /api/v1/admin/sso/providers
```

Supported out of the box: Microsoft Entra, Okta, Google Workspace, or any OIDC-compliant IdP. Then map directory groups to Wachd teams and roles — members are provisioned automatically on first login:

```
POST /api/v1/admin/group-mappings
```

#### Teams and users

Create teams for each engineering group, then add local users or grant SSO group access:

```
POST /api/v1/admin/teams
POST /api/v1/admin/users
POST /api/v1/admin/groups
POST /api/v1/admin/groups/{id}/access
```

---

### What team admins configure (not a deployment task)

The following are **team admin responsibilities**, done self-service after the platform is live. The superadmin does not configure these — each team owns their own setup:

| Feature | Who | Endpoint |
|---|---|---|
| GitHub / GitLab repos | Team admin | `POST /api/v1/teams/{teamId}/datasources` |
| Slack / Teams channels | Team admin | `POST /api/v1/teams/{teamId}/channels` |
| Email notifications | Team admin | `POST /api/v1/teams/{teamId}/channels` |
| Prometheus / Loki / Datadog | Team admin | `POST /api/v1/teams/{teamId}/datasources` |
| Webhook integrations | Team admin | `POST /api/v1/teams/{teamId}/webhooks` |
| On-call schedules | Team admin | `PUT /api/v1/teams/{teamId}/schedule` |
| Alert routing rules | Team admin | Team settings in the GUI |

A team admin can complete their full configuration in under 5 minutes without involving the platform superadmin.

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

## Tested Deployments

The following scenarios have been validated end-to-end by the maintainers:

| Scenario | Status |
|---|---|
| AWS EKS + RDS (PostgreSQL) + ElastiCache (Redis) | ✅ Tested |
| Azure AKS + Azure Database for PostgreSQL + Azure Cache for Redis | ✅ Tested |
| Grafana webhook → AI analysis → Slack notification | ✅ Tested |
| Grafana webhook → AI analysis → Email notification | ✅ Tested |
| On-call escalation with voice call via Twilio | ✅ Tested |
| Per-user notification rules (email now, voice after N min) | ✅ Tested |
| iOS mobile push notifications via APNs | ✅ Tested |
| Microsoft Entra SSO + group mappings | ✅ Tested |
| GKE | 🔲 Not yet tested |
| Datadog webhook (parser implemented) | 🔲 Not yet tested |
| Ollama with GPU nodes | 🔲 Not yet tested |
| Prometheus Alertmanager webhook | 🔲 Not yet tested |

If you test a scenario not listed above, please open an issue or PR to update this table.

---

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on reporting bugs, proposing features, and submitting pull requests.

---

## Community

Questions, feedback, or just want to follow the build?

- **Discord:** [discord.gg/zV86KNgkcJ](https://discord.gg/zV86KNgkcJ)
- **GitHub Issues:** [github.com/wachd/wachd/issues](https://github.com/wachd/wachd/issues)
- **X:** [@Wachd_io](https://x.com/Wachd_io)

---

## Security

To report a security vulnerability, see [SECURITY.md](SECURITY.md). Please do not open a public issue for security concerns.

---

## License

Apache 2.0. See [LICENSE](LICENSE) for the full text.

Copyright 2025 NTC Dev