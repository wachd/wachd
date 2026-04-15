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
- AKS cluster (3 nodes, Kubernetes 1.32)
- Azure Database for PostgreSQL Flexible Server
- Azure Cache for Redis (TLS, port 6380)
- Entra App Registration (SSO)
- Key Vault (stores all generated secrets)
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

## Step 6 — Create Kubernetes namespace and secrets

```bash
kubectl create namespace wachd
```

Create each secret from the Terraform outputs collected in Step 2:

```bash
# Encryption key (required — AES-256 for secrets at rest)
kubectl create secret generic wachd-encryption-key \
  --namespace wachd \
  --from-literal=encryption-key="$(cd infra/azure && terraform output -raw wachd_encryption_key)"

# PostgreSQL password
kubectl create secret generic wachd-db-secret \
  --namespace wachd \
  --from-literal=password="$(cd infra/azure && terraform output -raw postgres_password)"

# Redis password
kubectl create secret generic wachd-redis-secret \
  --namespace wachd \
  --from-literal=password="$(cd infra/azure && terraform output -raw redis_primary_key)"

# Entra client secret (for SSO)
kubectl create secret generic wachd-entra-secret \
  --namespace wachd \
  --from-literal=client-secret="$(cd infra/azure && terraform output -raw entra_client_secret)"
```

Optional secrets (enable features as needed):

```bash
# Slack notifications
kubectl create secret generic wachd-slack-webhook \
  --namespace wachd \
  --from-literal=url="https://hooks.slack.com/services/..."

# GitHub context collection
kubectl create secret generic wachd-github-token \
  --namespace wachd \
  --from-literal=token="ghp_..."

# Claude AI analysis (enterprise)
kubectl create secret generic wachd-claude-key \
  --namespace wachd \
  --from-literal=api-key="sk-ant-..."

# License key (SMB/Enterprise tier)
kubectl create secret generic wachd-license \
  --namespace wachd \
  --from-literal=license-key="..."
```

Verify all secrets exist:

```bash
kubectl get secrets -n wachd
```

---

## Step 7 — Update values-azure.yaml

Open `helm/wachd/values-azure.yaml` and replace every `<placeholder>` with the real values:

| Placeholder | Source |
|---|---|
| `<psql-wachd-prod.postgres.database.azure.com>` | `terraform output -raw postgres_host` |
| `<redis-wachd-prod.redis.cache.windows.net>` | `terraform output -raw redis_host` |
| `<your-entra-tenant-id>` | `terraform output -raw entra_tenant_id` |
| `<your-entra-client-id>` | `terraform output -raw entra_client_id` |
| `<wachd.yourdomain.com>` (all occurrences) | your `wachd_hostname` |

---

## Step 8 — Deploy Wachd via Helm

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

## Step 9 — Get bootstrap admin credentials

On first startup, Wachd creates a superadmin account and prints the credentials to the server log:

```bash
kubectl logs -n wachd \
  -l app.kubernetes.io/component=server \
  --tail=200 | grep -i "superadmin\|bootstrap\|password"
```

Log in at `https://<wachd_hostname>/` with those credentials.
You will be forced to change the password on first login.

---

## Step 10 — Grant Entra admin consent

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
