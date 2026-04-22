# Wachd — AWS EKS Enterprise Deployment

Manual runbook for deploying Wachd to AWS EKS. Follow each step in order.

**How secrets work here:** Secrets are managed via AWS Secrets Manager +
External Secrets Operator (ESO). You never run `kubectl create secret` — ESO syncs
secrets from Secrets Manager into Kubernetes automatically. To enable optional features
(Slack, GitHub, Claude AI), update the corresponding secret value in AWS Secrets Manager
and ESO will sync it within 1 hour.

> **Note:** The Azure deployment can use the same ESO pattern with Azure Key Vault
> (the Key Vault and Workload Identity are already provisioned by the Azure Terraform).
> The only difference is the `ClusterSecretStore` provider config — `aws` vs `azurekv`.
> The current Azure deployment uses `kubectl create secret` for simplicity, but ESO is
> the recommended approach for both clouds once you want parity.

---

## Prerequisites

Install these tools before starting:

```bash
# AWS CLI v2
brew install awscli      # or: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html

# kubectl
brew install kubectl

# Helm
brew install helm

# Terraform
brew install terraform

# jq (used to parse terraform outputs)
brew install jq
```

Configure AWS credentials:

```bash
aws configure              # or: export AWS_PROFILE=myprofile
aws sts get-caller-identity  # confirm the right account is active
```

---

## Step 1 — Configure Terraform variables

```bash
cd infra/aws
cp terraform.tfvars.example terraform.tfvars
```

Edit `terraform.tfvars` — minimum required fields:

```hcl
region         = "us-east-1"        # AWS region
environment    = "prod"

# Fill this after Step 5 — use nip.io for testing without a real domain
# e.g. "wachd.203.0.113.50.nip.io"
wachd_hostname = "wachd.YOURIP.nip.io"
```

> `terraform.tfvars` is git-ignored — never commit it.

---

## Step 2 — Provision AWS infrastructure

```bash
cd infra/aws

terraform init    # Downloads providers including gavinbunney/kubectl
terraform plan
terraform apply
```

Takes ~15–20 minutes. Provisions:
- VPC with 3 public + 3 private subnets across 3 AZs
- EKS cluster (3 nodes, Kubernetes 1.31, IRSA enabled)
- RDS PostgreSQL 16 (Multi-AZ optional)
- ElastiCache Redis 7 (TLS in-transit + auth token)
- Amazon Managed Prometheus (AMP) workspace
- Amazon Managed Grafana (optional)
- Cognito User Pool (optional, for SSO)
- AWS Secrets Manager entries for all Wachd secrets
- IAM role for ESO (IRSA — principle of least privilege, scoped to `wachd/prod/*`)
- External Secrets Operator installed via Helm
- `ClusterSecretStore` and `ExternalSecret` resources created automatically

When complete, read the outputs:

```bash
# Safe to read — no secrets
terraform output deployment_summary

# Sensitive — available if needed
terraform output -raw postgres_password
terraform output -raw wachd_encryption_key
terraform output secrets_manager_console_url   # direct link to your secrets in AWS Console
```

---

## Step 3 — Configure kubectl

```bash
# The exact command is in the terraform output
aws eks update-kubeconfig --region us-east-1 --name eks-wachd-prod

kubectl cluster-info
kubectl get nodes    # all nodes should show Ready
```

---

## Step 4 — Install cert-manager

```bash
# Install CRDs first
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.15.3/cert-manager.crds.yaml

helm repo add jetstack https://charts.jetstack.io --force-update

helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --version v1.15.3 \
  --set installCRDs=false \
  --wait

kubectl get pods -n cert-manager   # all 3 pods should be Running
```

Create the Let's Encrypt ClusterIssuer (replace the email):

```bash
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: you@yourcompany.com
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
      - http01:
          ingress:
            ingressClassName: nginx
            ingressTemplate:
              metadata:
                annotations:
                  nginx.ingress.kubernetes.io/ssl-redirect: "false"
EOF
```

---

## Step 5 — Install nginx ingress controller

```bash
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx --force-update

helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx \
  --create-namespace \
  --set controller.replicaCount=2 \
  --set controller.service.type=LoadBalancer \
  --wait

# Get the public IP or DNS name assigned by AWS (may take 1-2 min)
kubectl get svc ingress-nginx-controller -n ingress-nginx
```

Take the `EXTERNAL-IP` (or DNS hostname for AWS NLB/ALB) and update `terraform.tfvars`:

```hcl
wachd_hostname = "wachd.<EXTERNAL-IP>.nip.io"
# or for a real domain: wachd_hostname = "wachd.yourcompany.com"
```

Also update `helm/wachd/values-aws.yaml` with the same hostname.

---

## Step 6 — Verify ESO is syncing secrets

Terraform already installed ESO and created all `ExternalSecret` resources.
Verify the sync worked before deploying Wachd:

```bash
# Check ESO pods are running
kubectl get pods -n external-secrets

# Check the ClusterSecretStore is ready
kubectl get clustersecretstore aws-secrets-manager

# Check all ExternalSecrets are synced (STATUS should be SecretSynced)
kubectl get externalsecrets -n wachd

# Verify K8s secrets exist (created by ESO from Secrets Manager)
kubectl get secrets -n wachd
```

Expected output — all ExternalSecrets should show `SecretSynced`:
```
NAME                   STORE                 REFRESH INTERVAL   STATUS         READY
wachd-claude-key       aws-secrets-manager   1h                 SecretSynced   True
wachd-db-secret        aws-secrets-manager   1h                 SecretSynced   True
wachd-encryption-key   aws-secrets-manager   1h                 SecretSynced   True
wachd-github-token     aws-secrets-manager   1h                 SecretSynced   True
wachd-license          aws-secrets-manager   1h                 SecretSynced   True
wachd-oidc-secret      aws-secrets-manager   1h                 SecretSynced   True
wachd-redis-secret     aws-secrets-manager   1h                 SecretSynced   True
wachd-slack-webhook    aws-secrets-manager   1h                 SecretSynced   True
wachd-smtp-creds       aws-secrets-manager   1h                 SecretSynced   True
```

> **No `kubectl create secret` needed.** All K8s secrets are managed by ESO.

---

## Step 7 — Enable optional features via Secrets Manager

To enable Slack notifications, Claude AI, GitHub context collection, email, or a license key,
update the corresponding secret value in AWS Secrets Manager. ESO will sync the new
value into Kubernetes within 1 hour (or force an immediate sync — see below).

```bash
# Enable email notifications (any SMTP provider — Resend, SendGrid, SES, Mailgun, etc.)
# Store username and password as a JSON object; ESO extracts both keys automatically.
aws secretsmanager put-secret-value \
  --region us-east-1 \
  --secret-id wachd/prod/smtp-credentials \
  --secret-string '{"username":"resend","password":"re_..."}'

# Enable Slack notifications
aws secretsmanager put-secret-value \
  --region us-east-1 \
  --secret-id wachd/prod/slack-webhook-url \
  --secret-string "https://hooks.slack.com/services/T.../B.../..."

# Enable Claude AI backend
aws secretsmanager put-secret-value \
  --region us-east-1 \
  --secret-id wachd/prod/claude-api-key \
  --secret-string "sk-ant-..."

# Enable GitHub context collection
aws secretsmanager put-secret-value \
  --region us-east-1 \
  --secret-id wachd/prod/github-token \
  --secret-string "ghp_..."

# Apply a license key
aws secretsmanager put-secret-value \
  --region us-east-1 \
  --secret-id wachd/prod/license-key \
  --secret-string "WACHD-XXXX-XXXX-XXXX"

# Force immediate resync (instead of waiting 1h)
kubectl annotate externalsecret <secret-name> -n wachd \
  force-sync=$(date +%s) --overwrite
```

You can also update secrets via the AWS Console:
```
https://us-east-1.console.aws.amazon.com/secretsmanager/listsecrets?region=us-east-1
```

---

## Step 8 — Create Helm values for AWS

Copy the example file and fill in your real values from the Terraform outputs:

```bash
cp helm/wachd/values-aws.example.yaml helm/wachd/values-aws.yaml
```

Edit `helm/wachd/values-aws.yaml` and replace each `<placeholder>` value:

| Placeholder | Source |
|---|---|
| `<rds-wachd-prod.xxxx.us-east-1.rds.amazonaws.com>` | `terraform output -raw postgres_host` |
| `<master.redis-wachd-prod.xxxx.use1.cache.amazonaws.com>` | `terraform output -raw redis_host` |
| `<wachd.yourdomain.com>` (all occurrences) | your `wachd_hostname` |

> `helm/wachd/values-aws.yaml` is git-ignored — never commit it.

---

## Step 9 — Deploy Wachd via Helm

```bash
cd <repo root>

helm upgrade --install wachd ./helm/wachd \
  --namespace wachd \
  -f helm/wachd/values.yaml \
  -f helm/wachd/values-aws.yaml

kubectl get pods -n wachd -w   # watch pods come up — server, worker, web
kubectl get ingress -n wachd   # should show your hostname with an address
```

> **Do not use `--wait` or `--atomic`.** If old pods are in CrashLoopBackOff,
> `--wait` times out. Monitor with `kubectl get pods -n wachd -w` instead.

---

## Step 10 — Get bootstrap admin credentials

On first startup, Wachd creates a superadmin account and prints credentials to the server log:

```bash
kubectl logs -n wachd \
  -l app.kubernetes.io/component=server \
  --tail=200 | grep -i "superadmin\|bootstrap\|password"
```

Log in at `https://<wachd_hostname>/` with those credentials.
You will be forced to change the password on first login.

---

## Step 11 — Superadmin setup (via GUI or API)

**Everything in this step is done through the Wachd GUI or API — no redeployment needed.**

### AI backend

The AI backend is **platform-wide** — set once by the superadmin for the whole platform, not per team. You already set this in `values-aws.yaml` (`analysis.backend`). All teams use this backend.

> GUI/API management of the AI backend without redeploying is tracked in [wachd/wachd#1](https://github.com/wachd/wachd/issues/1).

### SSO / identity providers

Add your OIDC provider (Cognito, Okta, Entra, or any IdP) so engineers sign in with their existing accounts:

```
POST /api/v1/admin/sso/providers
```

Map directory groups to Wachd teams and roles — members are provisioned automatically on first login:

```
POST /api/v1/admin/group-mappings
```

### Teams and users

Create a team for each engineering group. Add local users, or grant SSO group access so teams self-provision:

```
POST /api/v1/admin/teams
POST /api/v1/admin/users
POST /api/v1/admin/groups
POST /api/v1/admin/groups/{id}/access
```

### What team admins configure (not a deployment task)

Once the platform is live, team admins own their own setup — the superadmin does not configure these:

| Feature | Endpoint |
|---|---|
| GitHub / GitLab repos | `POST /api/v1/teams/{teamId}/datasources` |
| Slack / Teams / email | `POST /api/v1/teams/{teamId}/channels` |
| Prometheus / Loki / Datadog | `POST /api/v1/teams/{teamId}/datasources` |
| Webhook integrations | `POST /api/v1/teams/{teamId}/webhooks` |
| On-call schedule | `PUT /api/v1/teams/{teamId}/schedule` |



---

## Verify the deployment

```bash
# Health check
curl -s https://<wachd_hostname>/api/v1/health | jq .

# Watch logs
kubectl logs -n wachd -l app.kubernetes.io/component=server -f
kubectl logs -n wachd -l app.kubernetes.io/component=worker -f

# Send a test webhook (replace TEAM_ID and SECRET from the UI or admin API)
curl -X POST https://<wachd_hostname>/api/v1/webhook/<TEAM_ID>/<SECRET> \
  -H "Content-Type: application/json" \
  -d '{
    "title": "High error rate — checkout-service",
    "state": "alerting",
    "message": "Error rate exceeded 5% threshold",
    "tags": { "service": "checkout-service", "env": "production" }
  }'
```

---

## Redis: AWS vs Azure difference

| | Azure | AWS |
|---|---|---|
| Port | 6379 (non-TLS, disabled) / **6380 (TLS)** | **6379 (TLS)** |
| TLS scheme | `rediss://host:6380` | `rediss://host:6379` |
| Auth | Primary access key (Bearer-style) | Auth token (password-style) |

**Important:** set `REDIS_PORT=6379` in your Helm values for AWS, not 6380.

---

## How ESO and Secrets Manager work together

```
Terraform apply
    │
    ├── Creates Secrets Manager secrets
    │     wachd/prod/postgres-password  → auto-generated
    │     wachd/prod/redis-auth-token   → auto-generated
    │     wachd/prod/encryption-key     → auto-generated
    │     wachd/prod/slack-webhook-url  → "placeholder"  ← DevOps updates this
    │     wachd/prod/claude-api-key     → "placeholder"  ← DevOps updates this
    │     wachd/prod/github-token       → "placeholder"  ← DevOps updates this
    │
    ├── Creates IAM role for ESO (IRSA, scoped to wachd/prod/* only)
    ├── Installs ESO via Helm (annotates service account with IAM role ARN)
    ├── Creates ClusterSecretStore → points to Secrets Manager in this region
    └── Creates ExternalSecret for each secret → maps SM path → K8s secret

ESO (running in cluster, every 1h)
    │
    ├── Reads each ExternalSecret
    ├── Calls secretsmanager:GetSecretValue (using IRSA — no static credentials)
    └── Creates/updates K8s secret in the wachd namespace

Wachd pods
    └── Read K8s secrets as environment variables (managed by Helm chart)
```

---

## Troubleshooting

### ExternalSecret stuck in "NotReady" or "SecretSyncError"

```bash
# Describe the failing ExternalSecret
kubectl describe externalsecret wachd-db-secret -n wachd

# Check ESO controller logs
kubectl logs -n external-secrets -l app.kubernetes.io/name=external-secrets -f

# Verify the IRSA role is correctly annotated on the ESO service account
kubectl get serviceaccount external-secrets -n external-secrets -o yaml | grep role-arn
```

**Common cause:** The ESO service account is missing the IRSA annotation, or the IAM role
trust policy has the wrong OIDC subject. Verify:
```bash
# Get the OIDC provider URL (strip https://)
aws eks describe-cluster --name eks-wachd-prod --region us-east-1 \
  --query "cluster.identity.oidc.issuer" --output text

# Verify the trust policy on the ESO role matches system:serviceaccount:external-secrets:external-secrets
aws iam get-role --role-name role-eso-wachd-prod --query "Role.AssumeRolePolicyDocument"
```

### TLS certificate stuck in False/Ready

```bash
kubectl describe challenge -n wachd
```

**Common cause on AWS:** The nginx ingress LoadBalancer provisions an AWS NLB/ELB, which
may take 2-3 minutes to become healthy. Let's Encrypt HTTP-01 challenges fail if the LB
isn't serving traffic yet.

```bash
# Verify port 80 responds (must be accessible from the internet)
curl -v http://<wachd_hostname>/.well-known/acme-challenge/test

# Force cert-manager to retry
kubectl delete certificaterequest -n wachd -l cert-manager.io/certificate-name=wachd-tls
```

### Pods crash with `context deadline exceeded` connecting to RDS or Redis

```bash
kubectl logs -n wachd -l app.kubernetes.io/component=server --previous
# Failed to connect to database: failed to ping database: context deadline exceeded
```

**Cause:** EKS automatically creates a cluster-managed security group (`eks-cluster-sg-*`) and
attaches it to all nodes. If the RDS/Redis security groups only allow traffic from a specific
node SG, pods are blocked because their traffic originates from the cluster SG, not the node SG.

This was fixed in the Terraform by allowing the full VPC CIDR on ports 5432 and 6379. RDS and
Redis are in private subnets — no public route exists — so VPC-scoped rules are the right boundary.

If you hit this on a cluster provisioned before this fix, run:
```bash
cd infra/aws
terraform apply
```

Terraform will update the RDS and Redis security group rules in place. No cluster or database
restart is needed — the rule change takes effect within seconds.

### Worker crashes with nil pointer on first alert

```bash
kubectl logs -n wachd -l app.kubernetes.io/component=worker -f
```

**Cause:** No on-call schedule configured on the default team.
**Fix:** Log in to Wachd, create an on-call schedule for the team, then the next alert will process successfully.

### Helm upgrade times out with `--wait`

Skip `--wait` — pods come up correctly regardless:
```bash
helm upgrade --install wachd ./helm/wachd \
  --namespace wachd \
  -f helm/wachd/values.yaml \
  -f helm/wachd/values-aws.yaml

kubectl get pods -n wachd -w
```

---

## Teardown

AWS manages ENIs for RDS, ElastiCache, and EKS internally. If you run a single `terraform destroy`,
Terraform may try to delete security groups before those ENIs are released — causing
`DependencyViolation` or `AuthFailure` errors. Use a two-phase destroy instead.

**Phase 1 — delete the data plane resources and wait for ENIs to release:**

```bash
helm uninstall wachd -n wachd
kubectl delete namespace wachd

cd infra/aws
terraform destroy \
  -target=aws_db_instance.wachd \
  -target=aws_elasticache_replication_group.wachd \
  -target=aws_eks_node_group.wachd

# Wait for AWS to fully release managed ENIs after RDS and ElastiCache deletion
sleep 90
```

**Phase 2 — destroy everything else:**

```bash
terraform destroy
```

> If Phase 2 still fails with a `DependencyViolation` on a security group, wait 60 seconds
> and re-run `terraform destroy`. EKS cluster deletion can take up to 15 minutes and may hold
> ENIs until it fully completes.
