# Mobile App

Wachd has native iOS and Android apps. This page covers push notification setup for both
**direct** (cloud-managed Wachd) and **relay** (self-hosted Wachd) deployment modes.

---

## Two Push Notification Modes

### Mode A — Direct (cloud-managed Wachd)

The Wachd server holds the APNs key and FCM service account directly and pushes to
Apple/Google on your behalf. Use this if you are running a cloud-managed Wachd instance
where you control the credentials.

```
Your Wachd server  →  Apple APNs / Google FCM  →  iPhone / Android
```

### Mode B — Relay (self-hosted Wachd)

The published app (`io.wachd.app`) is signed under the Wachd Apple Developer account.
Only the Wachd team holds the APNs key for that bundle ID. Self-hosted customers use
`push.wachd.io` — a hosted relay that forwards the push on their behalf.

```
Your self-hosted Wachd  →  push.wachd.io  →  Apple APNs / Google FCM  →  Device
```

**Security model — zero incident content through the relay:**
The relay never sees alert titles, severity, or body text. Your Wachd server sends only
the device tokens and an incident UUID. The app wakes on a silent push, fetches the full
incident directly from your Wachd server, and builds the notification locally. If your
server is unreachable, the app shows a generic fallback: "New alert — open Wachd to view
details."

---

## Mode A — Direct Configuration

Set these environment variables on your Wachd server (or in Helm `values.yaml` via a
Kubernetes Secret):

### iOS / APNs

| Variable | Description |
|---|---|
| `APNS_KEY_ID` | 10-character key ID from Apple Developer portal |
| `APNS_TEAM_ID` | 10-character Apple Team ID |
| `APNS_BUNDLE_ID` | `io.wachd.app` for the published app |
| `APNS_PRIVATE_KEY` | PEM-encoded ES256 private key (contents of the `.p8` file) |
| `APNS_PRODUCTION` | `"true"` for production APNs gateway; omit for sandbox |

Storing the PEM key in an env var — use `\n` for newlines:

```
APNS_PRIVATE_KEY="-----BEGIN PRIVATE KEY-----\nMIGTAgEA...\n-----END PRIVATE KEY-----"
```

### Android / FCM

| Variable | Description |
|---|---|
| `FCM_SERVICE_ACCOUNT_JSON` | Full contents of the Firebase service account JSON key file |

If any variable is missing, that notifier is disabled and the worker falls back to other
channels (Slack, email, SMS). No crash, no partial state.

---

## Mode B — Relay Setup (self-hosted)

### Step 1 — Generate an Ed25519 keypair

```bash
openssl genpkey -algorithm ed25519 -out wachd-push-private.pem
openssl pkey -in wachd-push-private.pem -pubout -out wachd-push-public.pem
```

Keep `wachd-push-private.pem` secret. Never commit it to git or put it in Helm values.

### Step 2 — Register with push.wachd.io

```bash
curl -X POST https://push.wachd.io/v1/register \
  -H "Content-Type: application/json" \
  -d "{
    \"email\": \"ops@company.com\",
    \"public_key\": \"$(openssl pkey -in wachd-push-public.pem -pubin -outform DER | base64)\"
  }"
```

Response:
```json
{ "data": { "deployment_id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" }, "error": null }
```

Save the `deployment_id` — you need it in Step 4. A confirmation email is sent to your
registered address.

### Step 3 — Store the private key in a Kubernetes Secret

```bash
kubectl create secret generic wachd-push-relay \
  -n <your-wachd-namespace> \
  --from-file=WACHD_PUSH_RELAY_PRIVATE_KEY=wachd-push-private.pem
```

### Step 4 — Set Helm values

```yaml
config:
  notifications:
    pushRelay:
      enabled: true
      relayURL: "https://push.wachd.io"
      deploymentID: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"  # from Step 2
      privateKeySecret: wachd-push-relay                    # Secret created in Step 3
      privateKeyKey: WACHD_PUSH_RELAY_PRIVATE_KEY
```

The chart injects `WACHD_PUSH_RELAY_URL`, `WACHD_PUSH_RELAY_DEPLOYMENT_ID`, and
`WACHD_PUSH_RELAY_PRIVATE_KEY` into the worker pod automatically when `enabled: true`.
The private key is never stored in Helm values — it is read from the Kubernetes Secret.

### Revoking a deployment

```bash
# Sign a revocation request with your private key (push.wachd.io verifies ownership)
curl -X DELETE https://push.wachd.io/v1/keys \
  -H "X-Wachd-Deployment-ID: <deployment_id>" \
  -H "X-Wachd-Timestamp: $(date +%s)" \
  -H "X-Wachd-Signature: <ed25519 signature>"
```

---

## iOS App

Available on the App Store (TestFlight during beta).

- QR code onboarding — scan the code in your Wachd dashboard Settings page
- Certificate pinning + Face ID / Touch ID authentication
- APNs device token registration on first login
- Silent push + on-device fetch — alert details fetched directly from your Wachd server
- Incident acknowledge, snooze, and resolve actions from the lock screen
- On-call schedule view

**Minimum iOS version:** 15.0

## Android App

In development. Server-side FCM integration is already built and uses the same relay
pattern as iOS.
