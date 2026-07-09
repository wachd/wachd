# Mobile App

> **Coming soon.** The iOS app is in active development. The server-side push notification infrastructure is complete and production-ready.

## How Push Notifications Work for Self-Hosted Deployments

APNs (Apple's push service) requires the sender to own the app's bundle ID. Because the Wachd iOS app is published under `io.wachd.app`, only the Wachd team can send push notifications to it — self-hosted customers cannot supply their own APNs credentials.

To solve this, Wachd provides a **hosted push relay** at `push.wachd.io`.

```
Your Wachd server  →  push.wachd.io  →  Apple APNs  →  iPhone
```

The relay holds the APNs key. Your server authenticates with a relay token. No Apple Developer account or APNs setup is required on your side.

## Getting a Relay Token

1. Go to **push.wachd.io/register** and enter your email
2. You receive a relay token (`wpr_...`) immediately
3. Add it to your Wachd deployment

```yaml
# Helm values.yaml
push:
  relayURL: https://push.wachd.io
  relayToken: wpr_xxxxxxxxxxxx
```

Or via environment variables:

```
WACHD_PUSH_RELAY_URL=https://push.wachd.io
WACHD_PUSH_RELAY_TOKEN=wpr_xxxxxxxxxxxx
```

That is all the configuration required. No APNs keys, no Apple Developer account.

## Push Notification Flow

When an alert fires and the on-call engineer has a mobile push rule configured:

1. Wachd worker looks up the user's registered device tokens
2. Sends the notification payload to `push.wachd.io/send` with the relay token
3. The relay validates the token, forwards to Apple APNs
4. The iOS device receives the push notification

The push payload includes the incident title, severity, and incident ID so the app can deep-link directly to the incident.

## iOS App

The native iOS app handles:

- QR code onboarding — scan the code shown in your Wachd dashboard Settings page to connect your instance
- Secure session authentication (HTTPS required)
- APNs device token registration on first login
- Incident push notifications with title and severity
- On-call schedule view
- Incident acknowledge, snooze, and resolve actions

**Minimum iOS version:** 15.0

## Android App

Coming after iOS. The server-side FCM integration is already built. The relay will support Android via FCM using the same token model.

## Relay Service — Self-Hosting (Advanced)

If you want full control, the relay is open-source and can be self-hosted. You would need your own Apple Developer account and APNs key, and would set `APNS_*` env vars directly on your Wachd server instead of using a relay token.
