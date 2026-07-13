# Mobile App

> **Coming soon.** The iOS app is in active development. The server-side push notification infrastructure is complete and production-ready.

## How Push Notifications Work

Wachd sends push notifications directly to Apple APNs using token-based authentication (ES256 JWT). You need an Apple Developer account and an APNs auth key to enable this.

```
Your Wachd server  →  Apple APNs  →  iPhone
```

## Configuration

Set the following environment variables on your Wachd server (or in your Helm `values.yaml` via a Kubernetes Secret):

| Variable | Description |
|---|---|
| `APNS_KEY_ID` | 10-character key ID from the Apple Developer portal |
| `APNS_TEAM_ID` | 10-character Apple Team ID |
| `APNS_BUNDLE_ID` | App bundle identifier — use `io.wachd.app` for the published app |
| `APNS_PRIVATE_KEY` | PEM-encoded ES256 private key (contents of the `.p8` file) |
| `APNS_PRODUCTION` | Set to `"true"` for the production APNs gateway; omit for sandbox |

**Storing the PEM key in an env var:** the key contains spaces in the header/footer lines. Quote the value and use `\n` for newlines:

```
APNS_PRIVATE_KEY="-----BEGIN PRIVATE KEY-----\nMIGTAgEA...\n-----END PRIVATE KEY-----"
```

If any variable is missing, APNs is disabled and the server falls back to other notification channels (Slack, email, SMS). No crash, no partial state.

## Push Notification Flow

When an alert fires and the on-call engineer has a registered device token:

1. Worker looks up the user's APNs device tokens
2. Signs a JWT using the ES256 key and sends the notification to APNs
3. APNs delivers the push to the iOS device

The payload includes the incident title, severity, and incident ID so the app deep-links directly to the incident. A notification action button lets the on-call engineer acknowledge the incident without opening the app.

## iOS App

The native iOS app handles:

- QR code onboarding — scan the code shown in your Wachd dashboard Settings page to connect to your instance
- Secure session authentication (HTTPS required)
- APNs device token registration on first login
- Incident push notifications with title and severity
- On-call schedule view
- Incident acknowledge, snooze, and resolve actions

**Minimum iOS version:** 15.0

## Android App

Coming after iOS. The server-side FCM integration is already built and uses the same pattern — set `FCM_*` env vars to enable Android push.
