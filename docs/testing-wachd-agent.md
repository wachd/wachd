# Testing wachd-agent with kind

End-to-end guide for verifying the wachd-agent Kubescape integration locally.
No real Kubernetes cluster required — everything runs in a kind cluster on your laptop.

---

## Prerequisites

| Tool | Version | Install |
|---|---|---|
| Docker | 24+ | https://docs.docker.com/get-docker |
| kind | 0.24+ | `brew install kind` |
| kubectl | 1.29+ | `brew install kubectl` |
| Helm | 3.15+ | `brew install helm` |
| Go | 1.22+ | https://go.dev/dl |

---

## 1. Run unit tests first

Before touching a cluster, run the full test suite:

```bash
cd /path/to/wachd
go test ./cmd/agent/... -race -count=1 -v
go test ./cmd/agent/collectors/kubescape/... -race -count=1 -v
```

Expected: all tests pass, no data races.

---

## 2. Create a kind cluster

```bash
kind create cluster --name wachd-agent-test
kubectl cluster-info --context kind-wachd-agent-test
```

---

## 3. Install Kubescape

Kubescape's Helm chart installs the operator and its CRDs.

```bash
helm repo add kubescape https://kubescape.github.io/helm-charts
helm repo update

helm install kubescape kubescape/kubescape-operator \
  --namespace kubescape \
  --create-namespace \
  --set capabilities.continuousScan=enable \
  --set capabilities.relevancyEnabled=true \
  --wait --timeout 5m
```

Verify the CRDs are installed:

```bash
kubectl get crd | grep kubescape
# Should include:
# vulnerabilitymanifestsummaries.spdx.softwarecomposition.kubescape.io
# workloadconfigurationscansummaries.spdx.softwarecomposition.kubescape.io
```

---

## 4. Build the agent image

```bash
# From repo root
docker build -f Dockerfile.agent -t wachd-agent:dev .

# Load into kind so the cluster can use it
kind load docker-image wachd-agent:dev --name wachd-agent-test
```

---

## 5. Start a fake Wachd endpoint

The agent POSTs to `/api/v1/webhook/{teamID}/{secret}`. Run a simple listener to capture those calls:

```bash
# In a separate terminal — leave it running
docker run --rm -p 8080:80 nginx:alpine &

# Or use a one-liner Go server that logs every request:
cat > /tmp/fake-wachd.go << 'EOF'
package main

import (
    "fmt"
    "io"
    "log"
    "net/http"
)

func main() {
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        log.Printf("→ %s %s\n%s\n", r.Method, r.URL.Path, body)
        w.WriteHeader(http.StatusOK)
    })
    fmt.Println("Listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
EOF
go run /tmp/fake-wachd.go
```

Find your laptop's IP on the Docker bridge so the kind pod can reach it:

```bash
# macOS
LAPTOP_IP=$(ipconfig getifaddr en0)
echo $LAPTOP_IP
```

---

## 6. Install wachd-agent via Helm

```bash
helm install wachd-agent ./helm/wachd-agent \
  --namespace wachd-agent \
  --create-namespace \
  --set image.repository=wachd-agent \
  --set image.tag=dev \
  --set image.pullPolicy=Never \
  --set wachd.endpoint="http://${LAPTOP_IP}:8080" \
  --set wachd.teamID="test-team" \
  --set wachd.webhookSecret="test-secret" \
  --set kubescape.enabled=true \
  --set kubescape.namespace=kubescape \
  --set kubescape.minSeverity=high

kubectl -n wachd-agent rollout status deployment/wachd-agent
```

Verify the agent is watching:

```bash
kubectl -n wachd-agent logs -f deploy/wachd-agent
# Should see:
# kubescape: watching vulnerabilitymanifestsummaries across all namespaces (rv=)
# kubescape: watching workloadconfigurationscansummaries across all namespaces (rv=)
```

Note: `rv=` (empty) is expected — Kubescape uses a custom storage backend that does not
return `metadata.resourceVersion` in list responses. The watch still receives events correctly.

---

## 7. Trigger a scan

Deploy a workload with known vulnerabilities:

```bash
# Deploy a deliberately old nginx with known CVEs
kubectl create deployment vuln-test \
  --image=nginx:1.19.0 \
  --namespace=default

kubectl scale deployment vuln-test --replicas=1 --namespace=default
```

Trigger Kubescape to scan immediately (instead of waiting for the scheduled scan):

```bash
# Manually trigger kubevuln (vulnerability scanner) via a one-off job
kubectl -n kubescape create job --from=cronjob/kubevuln-scheduler kubevuln-scan-now

# Wait for it to complete (~30s)
kubectl -n kubescape wait --for=condition=complete job/kubevuln-scan-now --timeout=120s
```

Note: `kubectl annotate namespace default kubescape.io/scan=true` does **not** trigger a scan
in kind clusters — use the job approach above.

Note: the `node-agent` pod will be in `CrashLoopBackOff` in kind — this is expected.
Without node-agent (eBPF), `.relevant` severity counts are absent and the agent falls back
to `.all` counts automatically.

Watch for summaries to appear in the **workload namespace** (not the kubescape namespace):

```bash
# Summaries appear in the namespace of the scanned workload, not in the kubescape namespace
kubectl get vulnerabilitymanifestsummaries -n default -w
kubectl get workloadconfigurationscansummaries -n default -w
```

---

## 8. Verify an event was forwarded

Once a summary object appears with `kubescape.io/status: ready`, the agent should forward it.

Check the agent logs:

```bash
kubectl -n wachd-agent logs -f deploy/wachd-agent
# Expected:
# → forwarded [kubescape/critical] Kubescape: 5 vulnerability finding(s) in default/vuln-test
```

Check the fake Wachd server (terminal from step 5):

```bash
# Expected output on the fake-wachd.go server:
# → POST /api/v1/webhook/test-team/test-secret
# {"title":"Kubescape: 5 vulnerability finding(s) in default/vuln-test","severity":"critical","source":"kubescape",...}
```

---

## 9. Verify deduplication

Bump the annotation on an existing summary (without changing findings) to trigger a re-reconcile:

```bash
kubectl annotate vulnerabilitymanifestsummary <name> -n default \
  kubescape.io/bump="$(date +%s)" --overwrite
```

The agent should **not** forward a second event for the same workload at the same finding count.
The logs should stay silent after the initial forward.

To trigger a re-fire, scale up the replica count (new workload scan):

```bash
kubectl scale deployment vuln-test --replicas=3 --namespace=default
# After the next scan completes, if finding count increases, a new event fires.
```

---

## 10. Verify minSeverity=critical filters high

Reinstall with `minSeverity=critical`:

```bash
helm upgrade wachd-agent ./helm/wachd-agent \
  --namespace wachd-agent \
  --set kubescape.minSeverity=critical \
  --reuse-values
```

After the next scan, only workloads with critical-severity findings should produce events.
High-only findings should be silently dropped — nothing forwarded, nothing logged.

---

## 11. Test RBAC is minimal

The agent ServiceAccount should only have access to Kubescape CRDs:

```bash
# Should succeed (has get/list/watch on summaries)
kubectl auth can-i list vulnerabilitymanifestsummaries \
  --as system:serviceaccount:wachd-agent:wachd-agent \
  --namespace kubescape
# → yes

# Should fail (no access to pods)
kubectl auth can-i list pods \
  --as system:serviceaccount:wachd-agent:wachd-agent
# → no
```

---

## 12. Cleanup

```bash
helm uninstall wachd-agent -n wachd-agent
helm uninstall kubescape -n kubescape
kind delete cluster --name wachd-agent-test
```

---

## Troubleshooting

**Agent not seeing scan results**

Check that the summary objects have `kubescape.io/status: ready`:
```bash
kubectl get vulnerabilitymanifestsummaries -n default -o json \
  | jq '.items[].metadata.annotations["kubescape.io/status"]'
```

If all values are empty or `"in-progress"`, Kubescape hasn't finished scanning yet.
Wait 2–3 minutes after deploying a workload.

**Agent pod in CrashLoopBackOff**

```bash
kubectl -n wachd-agent describe pod -l app.kubernetes.io/name=wachd-agent
kubectl -n wachd-agent logs deploy/wachd-agent --previous
```

Common causes: `WACHD_ENDPOINT` unreachable, missing RBAC (ClusterRole not bound).

**No events reaching the fake server**

Verify the agent can reach your laptop IP from inside the cluster:
```bash
kubectl run -it --rm curl-test --image=curlimages/curl --restart=Never -- \
  curl -v http://${LAPTOP_IP}:8080/
```

If connection refused: check your firewall allows port 8080 from the Docker bridge network.

**`nestedInt` returning 0 unexpectedly**

This would mean the severity counts are always 0. Inspect a raw summary object:
```bash
kubectl get vulnerabilitymanifestsummary <name> -n default \
  -o jsonpath='{.spec.severities}'
```

Summaries appear in the **workload namespace** (e.g. `default`), not in the `kubescape` namespace.
In kind clusters without network access, the kubevuln scanner may return all-zero counts — this is
a cluster limitation, not an agent bug. The agent correctly fires no event when counts are zero.
