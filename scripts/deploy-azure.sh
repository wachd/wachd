#!/usr/bin/env bash
# =============================================================================
# deploy-azure.sh — Full Wachd enterprise deployment to Azure AKS
#
# What this script does (in order):
#   1.  Checks prerequisites (az, kubectl, helm, terraform)
#   2.  Runs terraform apply to provision AKS, PostgreSQL, Redis, Entra, Key Vault
#   3.  Configures kubectl to point at the new AKS cluster
#   4.  Installs cert-manager (for TLS certificate automation)
#   5.  Installs nginx ingress controller (for external traffic routing)
#   6.  Creates Kubernetes namespace and all required secrets
#   7.  Patches values-azure.yaml with real Terraform outputs
#   8.  Runs helm upgrade --install
#   9.  Waits for pods to become ready
#   10. Prints bootstrap admin credentials and test commands
#
# Usage:
#   chmod +x scripts/deploy-azure.sh
#   ./scripts/deploy-azure.sh
#
# Optional environment variables:
#   WACHD_NAMESPACE      K8s namespace (default: wachd)
#   WACHD_RELEASE        Helm release name (default: wachd)
#   SLACK_WEBHOOK_URL    Slack webhook URL (creates K8s secret)
#   GITHUB_TOKEN         GitHub read-only token (creates K8s secret)
#   CLAUDE_API_KEY       Anthropic API key (creates K8s secret)
#   WACHD_LICENSE_KEY    License key for SMB/Enterprise tier
#   SKIP_TERRAFORM       Set to "true" to skip terraform apply (infra already exists)
#   DRY_RUN              Set to "true" to print commands without running them
# =============================================================================

set -euo pipefail

# ─── Configuration ────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
INFRA_DIR="${REPO_ROOT}/infra/azure"
HELM_DIR="${REPO_ROOT}/helm/wachd"
NAMESPACE="${WACHD_NAMESPACE:-wachd}"
RELEASE="${WACHD_RELEASE:-wachd}"
SKIP_TERRAFORM="${SKIP_TERRAFORM:-false}"
DRY_RUN="${DRY_RUN:-false}"

# Cert-manager version — check https://cert-manager.io/docs/releases/ for latest
CERT_MANAGER_VERSION="v1.15.3"
# nginx ingress controller version
NGINX_INGRESS_VERSION="4.11.3"

# ─── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

log()  { echo -e "${BLUE}[deploy]${RESET} $*"; }
ok()   { echo -e "${GREEN}[  ok  ]${RESET} $*"; }
warn() { echo -e "${YELLOW}[ warn ]${RESET} $*"; }
err()  { echo -e "${RED}[ fail ]${RESET} $*"; exit 1; }
step() { echo -e "\n${BOLD}${CYAN}══ Step $* ══${RESET}"; }
run()  {
  if [[ "${DRY_RUN}" == "true" ]]; then
    echo -e "${YELLOW}[dry-run]${RESET} $*"
  else
    eval "$@"
  fi
}

# ─── Step 1: Prerequisites ────────────────────────────────────────────────────
step "1/10 — Checking prerequisites"

check_cmd() {
  if ! command -v "$1" &>/dev/null; then
    err "Required tool '$1' not found. Install it and re-run.\n  $2"
  fi
  ok "$1 found: $(command -v "$1")"
}

check_cmd az        "https://docs.microsoft.com/en-us/cli/azure/install-azure-cli"
check_cmd kubectl   "https://kubernetes.io/docs/tasks/tools/"
check_cmd helm      "https://helm.sh/docs/intro/install/"
check_cmd jq        "https://stedolan.github.io/jq/download/"

if [[ "${SKIP_TERRAFORM}" != "true" ]]; then
  check_cmd terraform "https://developer.hashicorp.com/terraform/downloads"
fi

# Check Azure login
if ! az account show &>/dev/null; then
  err "Not logged in to Azure. Run: az login"
fi

SUBSCRIPTION_NAME=$(az account show --query name -o tsv)
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
ok "Azure subscription: ${SUBSCRIPTION_NAME} (${SUBSCRIPTION_ID})"

# Check terraform.tfvars exists
if [[ "${SKIP_TERRAFORM}" != "true" ]]; then
  if [[ ! -f "${INFRA_DIR}/terraform.tfvars" ]]; then
    warn "terraform.tfvars not found."
    log "Copy the example and fill in your values:"
    log "  cp ${INFRA_DIR}/terraform.tfvars.example ${INFRA_DIR}/terraform.tfvars"
    log "  vi ${INFRA_DIR}/terraform.tfvars"
    err "Create terraform.tfvars first."
  fi
  ok "terraform.tfvars found"
fi

# ─── Step 2: Terraform ────────────────────────────────────────────────────────
step "2/10 — Provisioning Azure infrastructure with Terraform"

if [[ "${SKIP_TERRAFORM}" == "true" ]]; then
  warn "SKIP_TERRAFORM=true — skipping terraform apply, reading existing state"
else
  cd "${INFRA_DIR}"
  run terraform init -upgrade
  run terraform validate

  log "Planning..."
  run terraform plan -out=tfplan

  echo ""
  echo -e "${BOLD}Review the plan above. Continue with apply? [y/N]${RESET} \c"
  read -r CONFIRM
  [[ "${CONFIRM}" =~ ^[Yy]$ ]] || err "Aborted."

  run terraform apply tfplan
  ok "Terraform apply complete"
fi

# ─── Step 3: Read Terraform outputs ──────────────────────────────────────────
step "3/10 — Reading Terraform outputs"

cd "${INFRA_DIR}"

TF_OUTPUT=$(terraform output -json)

get_output() {
  echo "${TF_OUTPUT}" | jq -r ".$1.value"
}

AKS_CLUSTER_NAME=$(get_output aks_cluster_name)
AKS_RESOURCE_GROUP=$(get_output aks_resource_group)
POSTGRES_HOST=$(get_output postgres_host)
POSTGRES_USER=$(get_output postgres_username)
POSTGRES_PASS=$(get_output postgres_password)
REDIS_HOST=$(get_output redis_host)
REDIS_PORT=$(get_output redis_port_tls)
REDIS_PASS=$(get_output redis_primary_key)
ENTRA_TENANT_ID=$(get_output entra_tenant_id)
ENTRA_CLIENT_ID=$(get_output entra_client_id)
ENTRA_CLIENT_SECRET=$(get_output entra_client_secret)
ENCRYPTION_KEY=$(get_output wachd_encryption_key)
KEY_VAULT_NAME=$(get_output key_vault_name)

log "AKS cluster:   ${AKS_CLUSTER_NAME}"
log "PostgreSQL:    ${POSTGRES_HOST}"
log "Redis:         ${REDIS_HOST}:${REDIS_PORT} (TLS)"
log "Entra tenant:  ${ENTRA_TENANT_ID}"
log "Entra app:     ${ENTRA_CLIENT_ID}"
log "Key Vault:     ${KEY_VAULT_NAME}"

# ─── Step 4: Configure kubectl ────────────────────────────────────────────────
step "4/10 — Configuring kubectl for AKS"

run "az aks get-credentials \
  --resource-group '${AKS_RESOURCE_GROUP}' \
  --name '${AKS_CLUSTER_NAME}' \
  --overwrite-existing"

run "kubectl cluster-info"
ok "kubectl configured for ${AKS_CLUSTER_NAME}"

# ─── Step 5: Install cert-manager ────────────────────────────────────────────
step "5/10 — Installing cert-manager ${CERT_MANAGER_VERSION}"

if kubectl get namespace cert-manager &>/dev/null; then
  ok "cert-manager namespace already exists — skipping install"
else
  run "kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.crds.yaml"

  run "helm repo add jetstack https://charts.jetstack.io --force-update"
  run "helm upgrade --install cert-manager jetstack/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --version ${CERT_MANAGER_VERSION} \
    --set installCRDs=false \
    --wait"

  ok "cert-manager ${CERT_MANAGER_VERSION} installed"
fi

# Create Let's Encrypt ClusterIssuer (prod)
# Replace EMAIL with your email for Let's Encrypt expiry notifications
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-admin@mycompany.com}"

# IMPORTANT: Use ingressClassName (not the deprecated class field).
# The deprecated class field causes cert-manager to create solver ingresses
# that the nginx controller ignores — HTTP-01 challenges will always fail.
#
# IMPORTANT: Add ssl-redirect: "false" to the solver ingress template.
# Without this, nginx redirects the ACME challenge HTTP request to HTTPS
# before Let's Encrypt can read the token, causing the challenge to fail.
cat <<EOF | run "kubectl apply -f -"
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: ${LETSENCRYPT_EMAIL}
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
ok "Let's Encrypt ClusterIssuer created"

# ─── Step 6: Install nginx ingress controller ─────────────────────────────────
step "6/10 — Installing nginx ingress controller ${NGINX_INGRESS_VERSION}"

if kubectl get namespace ingress-nginx &>/dev/null; then
  ok "ingress-nginx namespace already exists — skipping install"
else
  run "helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx --force-update"
  run "helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
    --namespace ingress-nginx \
    --create-namespace \
    --version ${NGINX_INGRESS_VERSION} \
    --set controller.service.annotations.'service\.beta\.kubernetes\.io/azure-load-balancer-health-probe-protocol'=tcp \
    --set controller.replicaCount=2 \
    --set controller.nodeSelector.'kubernetes\.io/os'=linux \
    --set defaultBackend.nodeSelector.'kubernetes\.io/os'=linux \
    --wait"

  ok "nginx ingress controller installed"
fi

# Get the public IP assigned to the ingress controller
log "Waiting for ingress LoadBalancer IP..."
for i in $(seq 1 30); do
  INGRESS_IP=$(kubectl get svc ingress-nginx-controller \
    -n ingress-nginx \
    -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
  if [[ -n "${INGRESS_IP}" ]]; then
    break
  fi
  sleep 5
done

if [[ -n "${INGRESS_IP}" ]]; then
  ok "Ingress public IP: ${INGRESS_IP}"
  echo ""
  echo -e "${BOLD}${YELLOW}ACTION REQUIRED:${RESET}"
  echo -e "  Create a DNS A record:  wachd.mycompany.com  →  ${INGRESS_IP}"
  echo -e "  Or for quick testing:  wachd.${INGRESS_IP}.nip.io"
  echo ""
else
  warn "Could not determine ingress IP yet — check manually with:"
  warn "  kubectl get svc ingress-nginx-controller -n ingress-nginx"
fi

# ─── Step 7: Create K8s namespace and secrets ─────────────────────────────────
step "7/10 — Creating K8s namespace and secrets in '${NAMESPACE}'"

run "kubectl create namespace '${NAMESPACE}' --dry-run=client -o yaml | kubectl apply -f -"

# Encryption key (required — AES-256 for secrets at rest)
run "kubectl create secret generic wachd-encryption-key \
  --namespace '${NAMESPACE}' \
  --from-literal=encryption-key='${ENCRYPTION_KEY}' \
  --dry-run=client -o yaml | kubectl apply -f -"
ok "Secret: wachd-encryption-key"

# PostgreSQL password
run "kubectl create secret generic wachd-db-secret \
  --namespace '${NAMESPACE}' \
  --from-literal=password='${POSTGRES_PASS}' \
  --dry-run=client -o yaml | kubectl apply -f -"
ok "Secret: wachd-db-secret"

# Redis password
run "kubectl create secret generic wachd-redis-secret \
  --namespace '${NAMESPACE}' \
  --from-literal=password='${REDIS_PASS}' \
  --dry-run=client -o yaml | kubectl apply -f -"
ok "Secret: wachd-redis-secret"

# Entra client secret
run "kubectl create secret generic wachd-entra-secret \
  --namespace '${NAMESPACE}' \
  --from-literal=client-secret='${ENTRA_CLIENT_SECRET}' \
  --dry-run=client -o yaml | kubectl apply -f -"
ok "Secret: wachd-entra-secret"

# Optional: Slack webhook URL
if [[ -n "${SLACK_WEBHOOK_URL:-}" ]]; then
  run "kubectl create secret generic wachd-slack-webhook \
    --namespace '${NAMESPACE}' \
    --from-literal=url='${SLACK_WEBHOOK_URL}' \
    --dry-run=client -o yaml | kubectl apply -f -"
  ok "Secret: wachd-slack-webhook"
else
  warn "SLACK_WEBHOOK_URL not set — Slack notifications disabled. Set and re-run to enable."
fi

# Optional: GitHub token
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
  run "kubectl create secret generic wachd-github-token \
    --namespace '${NAMESPACE}' \
    --from-literal=token='${GITHUB_TOKEN}' \
    --dry-run=client -o yaml | kubectl apply -f -"
  ok "Secret: wachd-github-token"
else
  warn "GITHUB_TOKEN not set — GitHub context collection disabled."
fi

# Optional: Claude API key
if [[ -n "${CLAUDE_API_KEY:-}" ]]; then
  run "kubectl create secret generic wachd-claude-key \
    --namespace '${NAMESPACE}' \
    --from-literal=api-key='${CLAUDE_API_KEY}' \
    --dry-run=client -o yaml | kubectl apply -f -"
  ok "Secret: wachd-claude-key"
else
  warn "CLAUDE_API_KEY not set — AI analysis will use Ollama (local). Set CLAUDE_API_KEY and update analysis.backend=claude in values-azure.yaml."
fi

# Optional: License key
if [[ -n "${WACHD_LICENSE_KEY:-}" ]]; then
  run "kubectl create secret generic wachd-license \
    --namespace '${NAMESPACE}' \
    --from-literal=license-key='${WACHD_LICENSE_KEY}' \
    --dry-run=client -o yaml | kubectl apply -f -"
  ok "Secret: wachd-license"
fi

# ─── Step 8: Patch values-azure.yaml with real values ────────────────────────
step "8/10 — Patching values-azure.yaml with Terraform outputs"

VALUES_AZURE="${HELM_DIR}/values-azure.yaml"
VALUES_PATCHED="${HELM_DIR}/values-azure-patched.yaml"
cp "${VALUES_AZURE}" "${VALUES_PATCHED}"

# Read wachd_hostname from tfvars
WACHD_HOSTNAME=$(grep '^wachd_hostname' "${INFRA_DIR}/terraform.tfvars" | sed 's/.*=\s*"\(.*\)"/\1/')
if [[ -z "${WACHD_HOSTNAME}" ]]; then
  warn "Could not read wachd_hostname from terraform.tfvars — using nip.io with ingress IP"
  WACHD_HOSTNAME="${INGRESS_IP:-UNKNOWN}.nip.io"
fi

sed -i.bak \
  -e "s|<psql-wachd-prod.postgres.database.azure.com>|${POSTGRES_HOST}|g" \
  -e "s|<redis-wachd-prod.redis.cache.windows.net>|${REDIS_HOST}|g" \
  -e "s|6380|${REDIS_PORT}|g" \
  -e "s|<your-tenant-id>|${ENTRA_TENANT_ID}|g" \
  -e "s|<your-client-id>|${ENTRA_CLIENT_ID}|g" \
  -e "s|<wachd.mycompany.com>|${WACHD_HOSTNAME}|g" \
  "${VALUES_PATCHED}"

ok "values-azure-patched.yaml written with real Azure endpoints"

# ─── Step 9: Helm install/upgrade ────────────────────────────────────────────
step "9/10 — Deploying Wachd via Helm"

run "helm upgrade --install '${RELEASE}' '${HELM_DIR}' \
  --namespace '${NAMESPACE}' \
  --create-namespace \
  -f '${HELM_DIR}/values.yaml' \
  -f '${VALUES_PATCHED}'"

ok "Helm release '${RELEASE}' deployed to namespace '${NAMESPACE}'"

# Wait for pods — do NOT use --wait/--atomic; they time out if old pods are in
# CrashLoopBackOff during a rollout. Check pod status manually instead.
log "Waiting for pods to become ready (up to 5 min)..."
run "kubectl rollout status deployment/wachd-server -n '${NAMESPACE}' --timeout=5m || true"
run "kubectl rollout status deployment/wachd-worker -n '${NAMESPACE}' --timeout=5m || true"

# ─── Step 10: Wait for pods + print bootstrap info ───────────────────────────
step "10/10 — Verifying deployment"

log "Pods in namespace ${NAMESPACE}:"
run "kubectl get pods -n '${NAMESPACE}'"

log "Services:"
run "kubectl get svc -n '${NAMESPACE}'"

log "Ingress:"
run "kubectl get ingress -n '${NAMESPACE}'"

# Retrieve bootstrap admin credentials from server logs
log "Retrieving bootstrap admin credentials..."
sleep 5  # Give server time to log credentials

BOOTSTRAP_LOG=$(kubectl logs -n "${NAMESPACE}" -l app.kubernetes.io/component=server \
  --tail=100 --timestamps 2>/dev/null | grep -i "superadmin\|bootstrap\|initial password" || true)

echo ""
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════════════${RESET}"
echo -e "${BOLD}${GREEN}  Wachd deployed successfully!${RESET}"
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════════════${RESET}"
echo ""
echo -e "  URL:     ${BOLD}https://${WACHD_HOSTNAME}${RESET}"
echo ""
if [[ -n "${BOOTSTRAP_LOG}" ]]; then
  echo -e "  ${BOLD}Bootstrap credentials from server logs:${RESET}"
  echo "${BOOTSTRAP_LOG}"
else
  echo -e "  ${YELLOW}Get bootstrap credentials from server logs:${RESET}"
  echo -e "    kubectl logs -n ${NAMESPACE} -l app.kubernetes.io/component=server --tail=200 | grep -i superadmin"
fi
echo ""
echo -e "  ${BOLD}Next steps:${RESET}"
echo -e "  1. ${YELLOW}Grant admin consent for GroupMember.Read.All:${RESET}"
echo -e "     $(get_output entra_admin_consent_url)"
echo ""
echo -e "  2. ${YELLOW}Run E2E tests:${RESET}"
echo -e "     WACHD_URL=https://${WACHD_HOSTNAME} ./scripts/test-e2e-azure.sh"
echo ""
echo -e "  3. ${YELLOW}Access admin panel:${RESET}"
echo -e "     https://${WACHD_HOSTNAME}/admin"
echo ""
echo -e "  ${BOLD}Useful commands:${RESET}"
echo -e "    kubectl logs -n ${NAMESPACE} -l app.kubernetes.io/component=server -f"
echo -e "    kubectl logs -n ${NAMESPACE} -l app.kubernetes.io/component=worker -f"
echo -e "    kubectl get pods -n ${NAMESPACE} -w"
echo ""
