# Wachd — Azure Enterprise Deployment

Manual runbook for deploying Wachd to Azure AKS. Follow each step in order.
Once validated manually, use `scripts/deploy-azure.sh` to automate the full flow.

---

## Prerequisites

Install these tools before starting:

```bash
# Azure CLI
brew install azure-cli          # or: https://docs.microsoft.com/en-us/cli/azure/install-azure-cli

# kubectl
brew install kubectl

# Helm
brew install helm

# Terraform
brew install terraform

# jq (used to parse terraform outputs)
brew install jq
```

Log in to Azure:

```bash
az login
az account show   # confirm the right subscription is active
```

---

## Step 1 — Configure Terraform variables

```bash
cd infra/azure
cp terraform.tfvars.example terraform.tfvars
```

Edit `terraform.tfvars` — minimum required fields:

```hcl
location            = "uksouth"       # Azure region
resource_group_name = "rg-wachd"
environment         = "prod"

# Fill this after Step 5 — use nip.io for testing without a real domain
# e.g. "wachd.20.1.2.3.nip.io"
wachd_hostname = "wachd.YOURIP.nip.io"
```

> `terraform.tfvars` is git-ignored — never commit it.

---

## Step 2 — Provision Azure infrastructure

```bash
cd infra/azure

terraform init
terraform plan
terraform apply
```

Takes ~10–15 minutes. Provisions:
- AKS cluster (3 nodes, Kubernetes 1.32, Workload Identity enabled)
- Azure Database for PostgreSQL Flexible Server
- Azure Cache for Redis (TLS, port 6380)
- Entra App Registration (SSO)
- Key Vault (stores all secrets — auto-populated by Terraform)
- User-Assigned Managed Identity for ESO (Workload Identity auth to Key Vault)
- External Secrets Operator installed via Helm
- `ClusterSecretStore` and `ExternalSecret` resources created automatically
- Log Analytics workspace

When complete, read the outputs you will need in later steps:

```bash
# Safe to read — no secrets
terraform output deployment_summary

# Sensitive — store these somewhere secure (also in Key Vault)
terraform output -raw postgres_host
terraform output -raw postgres_password
terraform output -raw redis_host
terraform output -raw redis_primary_key
terraform output -raw entra_tenant_id
terraform output -raw entra_client_id
terraform output -raw entra_client_secret
terraform output -raw wachd_encryption_key
```

---

## Step 3 — Configure kubectl

```bash
az aks get-credentials \
  --resource-group rg-wachd \
  --name aks-wachd-prod \
  --overwrite-existing

kubectl cluster-info
kubectl get nodes    # all 3 nodes should show Ready
```

---

## Step 4 — Install cert-manager

```bash
# Install CRDs first
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.15.3/cert-manager.crds.yaml

# Add Helm repo and install
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

kubectl get clusterissuer letsencrypt-prod
```

> **Why `ingressClassName` not `class`?**
> The `class: nginx` field is deprecated. nginx ingress controller 1.x ignores solver
> ingresses that use the old annotation and never adds them to its routing table.
> The HTTP-01 challenge token is never served, and Let's Encrypt validation always fails.
>
> **Why `ssl-redirect: "false"` on the solver ingress?**
> Without this, nginx redirects the HTTP-01 challenge request to HTTPS. Let's Encrypt
> follows the redirect but there is no certificate yet — the HTTPS request fails.
> The solver ingress needs to serve the token over plain HTTP.

---

## Step 5 — Install nginx ingress controller

```bash
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx --force-update

helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx \
  --create-namespace \
  --set controller.service.annotations.'service\.beta\.kubernetes\.io/azure-load-balancer-health-probe-protocol'=tcp \
  --set controller.service.annotations.'service\.beta\.kubernetes\.io/port_80_health-probe_protocol'=Tcp \
  --set controller.service.annotations.'service\.beta\.kubernetes\.io/port_443_health-probe_protocol'=Tcp \
  --set controller.replicaCount=2 \
  --set controller.nodeSelector.'kubernetes\.io/os'=linux \
  --set defaultBackend.nodeSelector.'kubernetes\.io/os'=linux \
  --wait

# Get the public IP Azure assigned (may take 1-2 min to appear)
kubectl get svc ingress-nginx-controller -n ingress-nginx
```

Take the `EXTERNAL-IP` value and go back to `terraform.tfvars`:

```hcl
# For testing without a real domain:
wachd_hostname = "wachd.<EXTERNAL-IP>.nip.io"

# For a real domain — create a DNS A record pointing to EXTERNAL-IP, then:
wachd_hostname = "wachd.yourcompany.com"
```

Update `values-azure.yaml` with the same hostname (replace all `<wachd.mycompany.com>` placeholders).

> **Why three health probe annotations instead of one?**
> AKS 1.24+ cloud-provider-azure reads `appProtocol: http/https` on nginx service
> ports and **overrides** the global `azure-load-balancer-health-probe-protocol`
> annotation, always creating HTTP probes for ports 80/443 regardless of the global
> setting. The per-port annotations (`port_80_health-probe_protocol` and
> `port_443_health-probe_protocol`) take precedence over `appProtocol` and correctly
> set TCP probes. Without these, Azure LB marks all nginx backends unhealthy and
> silently drops HTTP request data — TCP connects succeed but the ACME HTTP-01
> challenge token is never served, causing Let's Encrypt validation to time out.
> See: https://github.com/Azure/AKS/issues/3210

---

## Step 6 — Verify ESO is syncing secrets

Terraform already installed ESO and created all `ExternalSecret` resources pointing at
Azure Key Vault. Verify the sync worked before deploying Wachd:

```bash
# Check ESO pods are running
kubectl get pods -n external-secrets

# Check the ClusterSecretStore is ready
kubectl get clustersecretstore azure-key-vault

# Check all ExternalSecrets are synced (STATUS should be SecretSynced)
kubectl get externalsecrets -n wachd

# Verify K8s secrets exist (created by ESO from Key Vault)
kubectl get secrets -n wachd
```

Expected output — all ExternalSecrets should show `SecretSynced`:
```
NAME                   STORE              REFRESH INTERVAL   STATUS         READY
wachd-claude-key       azure-key-vault    1h                 SecretSynced   True
wachd-db-secret        azure-key-vault    1h                 SecretSynced   True
wachd-encryption-key   azure-key-vault    1h                 SecretSynced   True
wachd-entra-secret     azure-key-vault    1h                 SecretSynced   True
wachd-github-token     azure-key-vault    1h                 SecretSynced   True
wachd-license          azure-key-vault    1h                 SecretSynced   True
wachd-redis-secret     azure-key-vault    1h                 SecretSynced   True
wachd-slack-webhook    azure-key-vault    1h                 SecretSynced   True
```

> **No `kubectl create secret` needed.** All K8s secrets are managed by ESO.

---

## Step 7 — Enable optional features via Key Vault

To enable Slack notifications, Claude AI, GitHub context collection, or a license key,
update the corresponding secret value in Key Vault. ESO syncs the new value into
Kubernetes within 1 hour (or force an immediate sync — see below).

```bash
KV_NAME=$(cd infra/azure && terraform output -raw key_vault_name)

# Enable Slack notifications
az keyvault secret set --vault-name "$KV_NAME" \
  --name wachd-slack-webhook-url \
  --value "https://hooks.slack.com/services/T.../B.../..."

# Enable Claude AI backend
az keyvault secret set --vault-name "$KV_NAME" \
  --name wachd-claude-api-key \
  --value "sk-ant-..."

# Enable GitHub context collection
az keyvault secret set --vault-name "$KV_NAME" \
  --name wachd-github-token \
  --value "ghp_..."

# Apply a license key
az keyvault secret set --vault-name "$KV_NAME" \
  --name wachd-license-key \
  --value "WACHD-XXXX-XXXX-XXXX"

# Force immediate resync (instead of waiting 1h)
kubectl annotate externalsecret <secret-name> -n wachd \
  force-sync=$(date +%s) --overwrite
```

You can also update secrets via the Azure Portal — navigate to your Key Vault → Secrets.

---

## Step 8 — Update values-azure.yaml

Open `helm/wachd/values-azure.yaml` and replace every `<placeholder>` with the real values:

| Placeholder | Source |
|---|---|
| `<psql-wachd-prod.postgres.database.azure.com>` | `terraform output -raw postgres_host` |
| `<redis-wachd-prod.redis.cache.windows.net>` | `terraform output -raw redis_host` |
| `<your-entra-tenant-id>` | `terraform output -raw entra_tenant_id` |
| `<your-entra-client-id>` | `terraform output -raw entra_client_id` |
| `<wachd.yourdomain.com>` (all occurrences) | your `wachd_hostname` |

---

## Step 9 — Deploy Wachd via Helm

```bash
cd <repo root>

helm upgrade --install wachd ./helm/wachd \
  --namespace wachd \
  -f helm/wachd/values.yaml \
  -f helm/wachd/values-azure.yaml

kubectl get pods -n wachd -w   # watch pods come up — server, worker, web
kubectl get ingress -n wachd   # should show your hostname with an IP
```

> **Do not use `--wait` or `--atomic`.** If old pods are in CrashLoopBackOff during
> a rollout, `--wait` will time out waiting for them to terminate. Run without `--wait`
> and monitor pods with `kubectl get pods -n wachd -w` instead.

---

## Step 10 — Get bootstrap admin credentials

On first startup, Wachd creates a superadmin account and prints the credentials to the server log:

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

The AI backend is **platform-wide** — set once by the superadmin for the whole platform, not per team. You already set this in `values-azure.yaml` (`analysis.backend`). All teams use this backend.

> GUI/API management of the AI backend without redeploying is tracked in [wachd/wachd#1](https://github.com/wachd/wachd/issues/1).

### SSO / identity providers

Your Entra App Registration was created by Terraform. Register it as an SSO provider in Wachd so engineers can sign in with their Entra accounts:

```
POST /api/v1/admin/sso/providers
{
  "name": "Entra",
  "provider_type": "oidc",
  "issuer_url": "https://login.microsoftonline.com/<tenant-id>/v2.0",
  "client_id": "<entra_client_id>",
  "client_secret": "<entra_client_secret>",
  "scopes": ["openid", "email", "profile", "GroupMember.Read.All"]
}
```

Map Entra security groups to Wachd teams and roles — members are provisioned automatically on first login:

```
POST /api/v1/admin/group-mappings
```

### Teams and users

Create a team for each engineering group. Add local users, or rely on SSO group mappings for automatic provisioning:

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

## Step 12 — Grant Entra admin consent

The `GroupMember.Read.All` permission (used for automatic team provisioning from Entra groups) requires a one-time admin consent. Visit this URL as a Global Admin:

```
https://login.microsoftonline.com/<entra_tenant_id>/adminconsent?client_id=<entra_client_id>
```

Or get the full URL from:

```bash
cd infra/azure && terraform output deployment_summary
```

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

## Troubleshooting

### TLS certificate stuck in False/Ready

**Symptom:** `kubectl get certificate -n wachd` shows `READY=False` for `wachd-tls`.

**Diagnose:**
```bash
kubectl describe challenge -n wachd
```

**Cause 1 — Port 80 not reachable ("Timeout during connect"):**
Let's Encrypt validates HTTP-01 challenges from the public internet. Port 80 must be open.
```bash
# Check NSG rules — port 80 must be allowed inbound
az network nsg rule list \
  --nsg-name <aks-agentpool-XXXXX-nsg> \
  --resource-group MC_<rg>_<cluster>_<region> -o table
```
The AKS node NSG is in the MC_ resource group, not the main rg. AKS auto-creates an allow rule for the LB IP but you may need to add a broader allow for Let's Encrypt IPs.

**Cause 2 — Azure LB health probe marking nginx unhealthy:**
Azure creates an HTTP health probe for nginx that sends `GET /` to the NodePort. nginx returns 404 (no Host header match), Azure marks all backends unhealthy, and the LB silently drops all forwarded HTTP traffic. TCP connects (SYN/SYN-ACK succeeds through the LB) but HTTP requests never reach nginx.

Fix: Change the probe to TCP on both ports:
```bash
# Find the NSG and probe names
MC_RG="MC_rg-wachd_aks-wachd-prod_<region>"
az network lb probe list --lb-name kubernetes --resource-group "$MC_RG" -o table

# Change both probes to TCP
az network lb probe update \
  --lb-name kubernetes \
  --resource-group "$MC_RG" \
  --name <probe-name-TCP-80> \
  --protocol Tcp --port <nodeport-80> --request-path ""

az network lb probe update \
  --lb-name kubernetes \
  --resource-group "$MC_RG" \
  --name <probe-name-TCP-443> \
  --protocol Tcp --port <nodeport-443> --request-path ""
```
> **Permanent fix:** The `deploy-azure.sh` script and this README's Step 5 now install
> nginx ingress with three annotations: the global `azure-load-balancer-health-probe-protocol=tcp`
> plus per-port `port_80_health-probe_protocol=Tcp` and `port_443_health-probe_protocol=Tcp`.
> The per-port annotations are required on AKS 1.24+ because `appProtocol: http/https` on
> nginx service ports overrides the global annotation (Azure/AKS#3210).

**Cause 3 — ClusterIssuer using deprecated `class: nginx`:**
If the ClusterIssuer was created with `class: nginx` (old format), cert-manager creates
solver ingresses that the nginx controller ignores. The challenge token is never served.

Fix: Recreate the ClusterIssuer with `ingressClassName: nginx` (see Step 4 above).
Then force a retry:
```bash
# Delete the failed CertificateRequest — cert-manager recreates it automatically
kubectl delete certificaterequest -n wachd -l cert-manager.io/certificate-name=wachd-tls

# If cert-manager is in backoff (retries every 1h after failure), reset the counter
kubectl patch certificate wachd-tls -n wachd \
  --subresource=status --type=merge \
  -p '{"status":{"failedIssuanceAttempts":0,"lastFailureTime":null}}'
```

---

### HTTP requests time out (port 80 returns 0 bytes)

TCP connects but HTTP response never arrives. This is the Azure LB health probe issue
described above. Fix the probes to TCP (see Cause 2 in TLS section).

To confirm: from inside the cluster nginx responds correctly. From outside it doesn't.
```bash
# Inside cluster — should return 308 or 200
kubectl exec -n ingress-nginx deploy/ingress-nginx-controller -- \
  curl -s --max-time 5 http://wachd.<IP>.nip.io/

# Outside — should now also work after probe fix
curl -v --max-time 10 http://wachd.<IP>.nip.io/
```

---

### Worker crashes with nil pointer on first alert

**Symptom:** Worker pod restarts after first webhook fires. Logs show panic at `cmd/worker/main.go`.

**Cause:** `GetCurrentOnCall` returns `(nil, nil)` when no on-call schedule is configured
(e.g. fresh deployment with empty default team). The worker assumed non-nil on success.

**Fix:** Already patched in `cmd/worker/main.go`. Worker now logs a warning when no
schedule is configured and continues processing the alert without notification.

---

### Helm upgrade times out with `--wait`

If old pods are in CrashLoopBackOff, `--wait` times out waiting for them to terminate.

```bash
# Skip --wait — pods come up correctly regardless
helm upgrade --install wachd ./helm/wachd \
  --namespace wachd \
  -f helm/wachd/values.yaml \
  -f helm/wachd/values-azure.yaml

# Monitor manually
kubectl get pods -n wachd -w
```

---

## Teardown

```bash
helm uninstall wachd -n wachd
kubectl delete namespace wachd

cd infra/azure
terraform destroy
```
