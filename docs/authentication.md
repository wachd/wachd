# Authentication

Wachd supports two authentication modes that can be used simultaneously: **local users** (username and password) and **SSO** (OIDC-compatible providers such as Microsoft Entra ID, Okta, and Google Workspace). A third mechanism, **API tokens**, is available for programmatic access and is covered separately in [api-tokens.md](api-tokens.md).

---

## Table of Contents

- [Authentication modes](#authentication-modes)
- [Bootstrap admin](#bootstrap-admin)
- [Local users](#local-users)
- [Password policy](#password-policy)
- [Local groups](#local-groups)
- [SSO providers](#sso-providers)
- [Group mappings (SSO to team)](#group-mappings)
- [Admin panel reference](#admin-panel-reference)
- [Kubernetes setup](#kubernetes-setup)

---

## Authentication modes

| Mode | Use case | Requires |
|---|---|---|
| Local user | Bootstrap admin, service accounts, teams without SSO | `WACHD_ENCRYPTION_KEY` |
| SSO (OIDC) | Corporate directory integration (Entra, Okta, Google) | `WACHD_ENCRYPTION_KEY` + provider config |
| API token | CI pipelines, Terraform, curl | A local superadmin account |

Both modes are active at the same time. An operator can log in with a local password while engineers authenticate through SSO.

---

## Bootstrap admin

On the very first startup, when no local users exist, Wachd automatically creates a superadmin account and prints the generated credentials to standard output once.

```
╔════════════════════════════════════════════════╗
║      WACHD — BOOTSTRAP ADMIN CREATED          ║
╠════════════════════════════════════════════════╣
║  Username: wachd_admin                         ║
║  Password: <generated>                         ║
╠════════════════════════════════════════════════╣
║  Change this password immediately!             ║
║  POST /auth/local/login  (then /change-password)║
╚════════════════════════════════════════════════╝
```

The password is never stored in plaintext. If you miss the output, reset it through the admin panel or via the API:

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/v1/admin/users/<id>/reset-password \
  -H "Content-Type: application/json" \
  -d '{"new_password": "NewPassword1!"}'
```

After login the server will require a password change before any other action is permitted.

### Kubernetes

In Kubernetes the bootstrap output appears in the server pod logs on first deployment:

```bash
kubectl logs -n wachd deployment/wachd-server --tail=50 | grep -A 10 "BOOTSTRAP"
```

Capture it before the pod restarts or the log buffer rolls over.

---

## Local users

Local users are managed exclusively by superadmins through the admin panel (`/admin/users`) or the REST API. They are global auth identities — not scoped to a team. Team access is granted through group membership (see [Local groups](#local-groups)).

### Creating a user

**Admin panel:** Navigate to Admin → Users → New User. Fill in username, email, and display name. A secure password is generated automatically and shown once in a banner after the user is created. Copy it before dismissing — it cannot be retrieved again.

**API:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/admin/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "alice",
    "email": "alice@example.com",
    "name": "Alice Smith",
    "password": "InitialPass1!",
    "is_superadmin": false
  }'
```

All admin-created users have `force_password_change: true`. The user must change their password on first login before accessing any other endpoint.

### Managing users

| Action | Admin panel | API endpoint |
|---|---|---|
| List users | Admin → Users | `GET /api/v1/admin/users` |
| Get user | Admin → Users → (row) | `GET /api/v1/admin/users/{id}` |
| Update email / name | Admin → Users → Edit | `PUT /api/v1/admin/users/{id}` |
| Deactivate / activate | Admin → Users → Deactivate | `PUT /api/v1/admin/users/{id}` `{"is_active": false}` |
| Reset password | — | `POST /api/v1/admin/users/{id}/reset-password` |
| Delete user | Admin → Users → Delete | `DELETE /api/v1/admin/users/{id}` |

Deactivating a user immediately invalidates all their active sessions and API tokens. Tokens are not deleted — they remain listed but will be rejected on the next request.

### Changing your own password

Users change their own password at `/change-password` in the UI or via the API:

```bash
curl -b cookies.txt -X POST http://localhost:8080/auth/local/change-password \
  -H "Content-Type: application/json" \
  -d '{
    "current_password": "OldPassword1!",
    "new_password": "NewPassword1!"
  }'
```

The new password must satisfy the active [password policy](#password-policy).

---

## Password policy

A single password policy applies to all local users. Superadmins configure it through Admin → Password Policy or via the API.

### Default values

| Setting | Default |
|---|---|
| Minimum length | 12 |
| Require uppercase | Yes |
| Require lowercase | Yes |
| Require number | Yes |
| Require special character | Yes |
| Maximum failed attempts before lockout | 5 |
| Lockout duration | 30 minutes |

### Updating the policy

**Admin panel:** Admin → Password Policy → edit values → Save.

**API:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X PUT http://localhost:8080/api/v1/admin/password-policy \
  -H "Content-Type: application/json" \
  -d '{
    "min_length": 16,
    "max_failed_attempts": 3,
    "lockout_duration_minutes": 60
  }'
```

Only the fields you include are updated. Omitted fields retain their current values.

The policy is applied at:
- Login (lockout enforcement)
- Password change (complexity validation)
- User creation (complexity validation)
- Admin password reset (complexity validation)

---

## Local groups

Local groups are superadmin-managed collections of local users that are granted access to one or more teams with a specific role. They are independent of SSO groups.

**Typical use case:** You have a "DevOps" team in your organisation. You create a local group named `devops`, add the relevant local users as members, and grant the group `responder` access to the production team. Every member of the group gets that access on login.

### Group roles

| Role | Capabilities |
|---|---|
| `viewer` | Read incidents and schedules |
| `responder` | Acknowledge, resolve, snooze incidents |
| `admin` | All responder capabilities plus manage team configuration |

### Creating a group

**Admin panel:** Admin → Groups → New Group.

**API:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/admin/groups \
  -H "Content-Type: application/json" \
  -d '{"name": "devops", "description": "DevOps engineers"}'
```

### Managing group members

**Admin panel:** Admin → Groups → Manage → Members tab.

**API:**

```bash
# Add a member
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/admin/groups/{groupId}/members \
  -H "Content-Type: application/json" \
  -d '{"user_id": "<userId>"}'

# Remove a member
curl -H "Authorization: Bearer wachd_..." \
  -X DELETE http://localhost:8080/api/v1/admin/groups/{groupId}/members/{userId}
```

### Granting team access to a group

**Admin panel:** Admin → Groups → Manage → Team Access tab → Grant Access.

**API:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/admin/groups/{groupId}/access \
  -H "Content-Type: application/json" \
  -d '{"team_id": "<teamId>", "role": "responder"}'
```

Access takes effect on the user's next login. Existing sessions are not updated until the session expires.

---

## SSO providers

Wachd stores OIDC provider configuration in the database with the client secret encrypted using AES-256-GCM. Providers can be added, updated, and enabled or disabled through the admin panel without restarting the server.

Supported provider types: any OIDC-compliant identity provider, including Microsoft Entra ID (formerly Azure AD), Okta, Google Workspace, Keycloak, and Auth0.

### Adding a provider

**Admin panel:** Admin → SSO Providers → New Provider.

**API:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/admin/sso/providers \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Corporate Entra",
    "issuer_url": "https://login.microsoftonline.com/<tenant-id>/v2.0",
    "client_id": "<application-client-id>",
    "client_secret": "<client-secret>",
    "scopes": ["openid", "profile", "email", "offline_access"],
    "enabled": true,
    "auto_provision": true
  }'
```

`auto_provision: true` creates a Wachd session for any authenticated user in the directory. Set it to `false` to require explicit group mappings before a user can log in.

### Testing a provider

After creating a provider, verify that Wachd can reach the OIDC discovery endpoint:

**Admin panel:** Admin → SSO Providers → Manage → Test Connection.

**API:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/admin/sso/providers/{id}/test
# Response: {"status": "ok"} or {"status": "error", "error": "<detail>"}
```

### Updating a provider

You can rotate a client secret or change any field without restarting the server. The in-memory provider cache is invalidated immediately after every update.

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X PUT http://localhost:8080/api/v1/admin/sso/providers/{id} \
  -H "Content-Type: application/json" \
  -d '{"client_secret": "<new-secret>"}'
```

### SSO login flow

When a user clicks an SSO provider button on the login page:

1. Browser is redirected to `/auth/sso/{providerId}/login`
2. Wachd loads the provider config from the DB cache and builds the OIDC authorization URL with `prompt=select_account`, which forces the identity provider's account picker on every login — preventing silent sign-in with a cached session
3. User authenticates with the identity provider
4. Provider redirects back to `/auth/sso/{providerId}/callback`
5. Wachd exchanges the code for tokens, reads the user profile
6. Group memberships from the ID token are applied against [group mappings](#group-mappings) to determine team access
7. Session is created and the session cookie is set

### Microsoft Entra ID — required app registration scopes

| Scope | Purpose |
|---|---|
| `openid` | Required for OIDC |
| `profile` | Name and username |
| `email` | Email address |
| `offline_access` | Refresh token |
| `GroupMember.Read.All` | Read group memberships (for group mappings) |

The redirect URI to register in Entra: `https://<your-domain>/auth/sso/<provider-id>/callback`.

#### Email claim behaviour

Microsoft Entra does not always populate the standard `email` claim even when the `email` scope is requested. Wachd resolves the user's email in this order:

1. `email` claim (standard OIDC)
2. `preferred_username` claim (Entra UPN — most reliable for work accounts)
3. `unique_name` claim (older Entra v1 token format)

If none of these are present the email field will be empty. Ensure your Entra app registration emits at least one of these claims (the `profile` scope covers `preferred_username`).

### Migrating from environment-variable SSO config

If you previously configured Entra via `ENTRA_TENANT_ID`, `ENTRA_CLIENT_ID`, and `ENTRA_CLIENT_SECRET` environment variables, Wachd automatically migrates those values to the `sso_providers` table on first startup with the encryption key present. After migration the env vars are no longer read. You can remove them from your deployment at the next upgrade.

---

## Group mappings

Group mappings connect an SSO provider's directory groups (identified by object ID or group claim) to a Wachd team and role. When a user logs in via SSO, Wachd reads their group memberships from the token claims and applies any matching mappings.

Group mappings are configured per team: Admin → Teams → Manage → Group Mappings.

### Adding a mapping

**Admin panel:** Admin → Teams → Manage → Add Mapping.

Fields:
- **SSO Provider** — the provider whose groups to match. The provider's UUID is stored as `sso_provider_id` on the mapping and is used for exact matching at login. Required when any SSO provider exists.
- **AD Group Object ID** — the directory group identifier (UUID in Entra, group claim value in other providers)
- **Display name** — optional label for human reference
- **Role** — `viewer`, `responder`, or `admin`

**API:**

Obtain the SSO provider's UUID from `GET /api/v1/admin/sso/providers` first.

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/admin/group-mappings \
  -H "Content-Type: application/json" \
  -d '{
    "provider_id": "<sso-provider-uuid>",
    "group_id": "<entra-group-object-id>",
    "group_name": "SRE Team",
    "team_id": "<wachd-team-id>",
    "role": "responder"
  }'
```

`provider_id` is the UUID of the SSO provider record, not the provider's display name or issuer URL. Mappings without a `provider_id` are legacy and will not match any login.

### How group resolution works at login

1. The OIDC ID token contains a `groups` claim with the user's group object IDs
2. Wachd looks up all group mappings where `sso_provider_id` matches the authenticating provider's UUID **and** `group_id` is in the token's `groups` claim — trailing whitespace in stored group IDs is trimmed before comparison
3. For each match, the user receives the mapped role on the mapped team
4. If the same user matches multiple mappings for the same team, the highest-privilege role wins (`admin` > `responder` > `viewer`)
5. Team access is stored in the session for the duration of the session TTL (24 hours)

---

## Admin panel reference

The admin panel is accessible at `/admin` and is visible only to superadmin accounts. It is hidden from regular users.

| Section | Path | Purpose |
|---|---|---|
| Overview | `/admin` | Summary of users, groups, and providers |
| Teams | `/admin/teams` | Create teams, manage group mappings |
| Users | `/admin/users` | Create and manage local users |
| Groups | `/admin/groups` | Create local groups, add members, grant team access |
| SSO Providers | `/admin/sso` | Configure OIDC providers |
| Password Policy | `/admin/password-policy` | View and edit password requirements |
| API Tokens | `/admin/tokens` | Generate and revoke personal access tokens |

---

## Kubernetes setup

### Encryption key

The encryption key protects SSO client secrets stored in the database. It must be a 32-byte value encoded as a 64-character hexadecimal string.

Generate the key:

```bash
openssl rand -hex 32
```

Store it as a Kubernetes secret before deploying the chart:

```bash
kubectl create secret generic wachd-encryption-key \
  --from-literal=encryption-key="$(openssl rand -hex 32)" \
  -n wachd
```

The chart references this secret automatically. Without it, the server refuses to start (except in `AUTH_DISABLED=true` development mode).

### Helm values

```yaml
auth:
  encryptionKeySecret:
    name: wachd-encryption-key  # name of the k8s Secret
    key: encryption-key          # key within the Secret
```

### OIDC redirect URI

Set the redirect base URI so Wachd can construct correct callback URLs for each provider:

```yaml
auth:
  redirectBaseURL: "https://wachd.example.com"
```

This produces callback URLs of the form `https://wachd.example.com/auth/sso/<providerId>/callback`.

Register each such URL in your identity provider's application registration.

### Environment variables (reference)

| Variable | Required | Description |
|---|---|---|
| `WACHD_ENCRYPTION_KEY` | Yes (unless `AUTH_DISABLED=true`) | 64-char hex encryption key for SSO secrets |
| `AUTH_DISABLED` | No | Set `true` for local development without auth |
| `CORS_ORIGIN` | No | Allowed origin for browser requests (default: `http://localhost:3000`) |
