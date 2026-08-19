#!/usr/bin/env bash
# helm/tests/push-relay-render.sh
#
# Verifies that the pushRelay Helm configuration is correctly wired into the
# worker Deployment. Run from the repo root:
#
#   bash helm/tests/push-relay-render.sh

set -euo pipefail

CHART="helm/wachd"
BASE_FLAGS=(
  --set auth.encryptionKeySecret.name=dummy
  --set auth.encryptionKeySecret.key=dummy
)

pass=0
fail=0

check() {
  local desc="$1"; shift
  if "$@"; then
    echo "  PASS: $desc"
    ((++pass))
  else
    echo "  FAIL: $desc"
    ((++fail))
  fi
}

echo "--- pushRelay disabled (default) ---"
DISABLED=$(helm template test "$CHART" "${BASE_FLAGS[@]}" \
  --set config.notifications.pushRelay.enabled=false)

check "no WACHD_PUSH_RELAY_URL when disabled" \
  bash -c "! echo '$DISABLED' | grep -q WACHD_PUSH_RELAY_URL"
check "no WACHD_PUSH_RELAY_DEPLOYMENT_ID when disabled" \
  bash -c "! echo '$DISABLED' | grep -q WACHD_PUSH_RELAY_DEPLOYMENT_ID"
check "no WACHD_PUSH_RELAY_PRIVATE_KEY when disabled" \
  bash -c "! echo '$DISABLED' | grep -q WACHD_PUSH_RELAY_PRIVATE_KEY"

echo ""
echo "--- pushRelay enabled ---"
ENABLED=$(helm template test "$CHART" "${BASE_FLAGS[@]}" \
  --set config.notifications.pushRelay.enabled=true \
  --set config.notifications.pushRelay.deploymentID=test-deploy-uuid \
  --set config.notifications.pushRelay.relayURL=https://push.wachd.io \
  --set config.notifications.pushRelay.privateKeySecret=wachd-push-relay \
  --set config.notifications.pushRelay.privateKeyKey=WACHD_PUSH_RELAY_PRIVATE_KEY)

check "WACHD_PUSH_RELAY_URL present when enabled" \
  bash -c "echo '$ENABLED' | grep -q 'WACHD_PUSH_RELAY_URL'"
check "WACHD_PUSH_RELAY_URL has correct value" \
  bash -c "echo '$ENABLED' | grep -q 'value: \"https://push.wachd.io\"'"
check "WACHD_PUSH_RELAY_DEPLOYMENT_ID present when enabled" \
  bash -c "echo '$ENABLED' | grep -q 'WACHD_PUSH_RELAY_DEPLOYMENT_ID'"
check "WACHD_PUSH_RELAY_DEPLOYMENT_ID has correct value" \
  bash -c "echo '$ENABLED' | grep -q 'value: \"test-deploy-uuid\"'"
check "WACHD_PUSH_RELAY_PRIVATE_KEY uses secretKeyRef (not plain value)" \
  bash -c "echo '$ENABLED' | grep -q 'secretKeyRef'"
check "private key secret name is correct" \
  bash -c "echo '$ENABLED' | grep -q 'name: wachd-push-relay'"

echo ""
echo "--- pushRelay enabled without deploymentID must fail ---"
MISSING_ID_OUTPUT=$(helm template test "$CHART" "${BASE_FLAGS[@]}" \
  --set config.notifications.pushRelay.enabled=true \
  --set config.notifications.pushRelay.deploymentID="" \
  2>&1) || true
if echo "$MISSING_ID_OUTPUT" | grep -q "deploymentID is required"; then
  echo "  PASS: missing deploymentID produces required error"
  ((++pass))
else
  echo "  FAIL: missing deploymentID should produce required error"
  ((++fail))
fi

echo ""
echo "Results: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
