# Wachd Helm Chart

**Wachd** is a self-hosted alert intelligence platform that tells you *why* an alert fired, not just *that* it fired. When your team gets paged at 3am, Wachd delivers probable root cause, a suggested action, and a link to the last similar incident — before the responder opens a terminal.

Self-hosted OpsGenie replacement with AI-powered root cause analysis, flexible on-call scheduling, and full REST API.

## Prerequisites

- Kubernetes 1.25+
- Helm 3.10+
- PostgreSQL 13+ (external recommended for production)
- Redis 6+ (external recommended for production)
- [cert-manager](https://cert-manager.io/docs/installation/) installed in the cluster

## Install

```bash
helm repo add wachd https://charts.wachd.io
helm repo update

helm install wachd wachd/wachd \
  --namespace wachd \
  --create-namespace \
  --set postgres.external.host=<db-host> \
  --set postgres.external.username=<db-user> \
  --set redis.external.host=<redis-host> \
  --set analysis.backend=ollama \
  --set auth.frontendURL=https://wachd.example.com \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=wachd.example.com
```

## Secrets

Create these before running `helm install`:

```bash
# Database password
kubectl create secret generic wachd-db-secret \
  --namespace wachd \
  --from-literal=password=<db-password>

# Encryption key (required — used for SSO client secrets stored in DB)
kubectl create secret generic wachd-encryption-key \
  --namespace wachd \
  --from-literal=encryption-key="$(openssl rand -hex 32)"

# Redis password (skip if no auth)
kubectl create secret generic wachd-redis-secret \
  --namespace wachd \
  --from-literal=password=<redis-password>

# AI backend key — choose one:
kubectl create secret generic wachd-claude-key \
  --namespace wachd --from-literal=api-key=<anthropic-key>

kubectl create secret generic wachd-openai-key \
  --namespace wachd --from-literal=api-key=<openai-key>
```

## AI Backend

Set `analysis.backend` to one of:

| Value | Description |
|---|---|
| `ollama` | Local LLM — air-gapped, no external API calls (default) |
| `claude` | Anthropic Claude — highest quality analysis |
| `openai` | OpenAI GPT-4o |
| `gemini` | Google Gemini — free tier available |

For air-gapped deployments, set `analysis.ollama.enabled: true` to deploy Ollama in-cluster alongside Wachd.

## Authentication

Wachd supports local users and SSO (OIDC). Configure the provider under `auth`:

```yaml
auth:
  frontendURL: https://wachd.example.com
  provider: entra   # entra | google | okta
  entra:
    tenantId: <azure-tenant-id>
    clientId: <app-client-id>
    clientSecretRef:
      name: wachd-entra-secret
      key: client-secret
```

A local superadmin account is always available as a break-glass login, even if SSO is down.

## Key Configuration

| Parameter | Description | Default |
|---|---|---|
| `image.tag` | Wachd image version | `0.4.17` |
| `server.replicaCount` | API server replicas | `2` |
| `analysis.backend` | AI engine | `ollama` |
| `analysis.ollama.model` | Ollama model to use | `llama3.2` |
| `postgres.enabled` | Deploy in-cluster Postgres | `false` |
| `redis.enabled` | Deploy in-cluster Redis | `false` |
| `ingress.enabled` | Create ingress resource | `false` |
| `ingress.hosts[0].host` | Public hostname | `wachd.example.com` |
| `license.type` | `opensource` / `smb` / `enterprise` | `opensource` |

Full configuration reference: [`values.yaml`](./values.yaml)

## Upgrading

```bash
helm repo update
helm upgrade wachd wachd/wachd --namespace wachd --reuse-values
```

Schema migrations run automatically on startup — no manual migration step required.

## Uninstall

```bash
helm uninstall wachd --namespace wachd
kubectl delete namespace wachd
```

## Links

- [GitHub](https://github.com/wachd/wachd)
- [Documentation](https://wachd.io)
- [Report an issue](https://github.com/wachd/wachd/issues)
