# API Tokens

API tokens (personal access tokens) allow programmatic access to the Wachd API using an `Authorization: Bearer` header instead of a browser session cookie. They are intended for CI/CD pipelines, Terraform providers, monitoring scripts, and any automation that needs to call Wachd without an interactive login.

---

## Table of Contents

- [Requirements](#requirements)
- [Creating a token](#creating-a-token)
- [Using a token](#using-a-token)
- [Listing tokens](#listing-tokens)
- [Revoking a token](#revoking-a-token)
- [Token scope and permissions](#token-scope-and-permissions)
- [Security model](#security-model)
- [CI/CD examples](#cicd-examples)
- [Testing](#testing)

---

## Requirements

- You must be a **local superadmin** to create and manage API tokens. SSO-only users cannot create tokens.
- The server must be running with `WACHD_ENCRYPTION_KEY` set (local auth enabled).

---

## Creating a token

### Admin panel

1. Log in as a superadmin.
2. Navigate to **Admin → API Tokens**.
3. Click **Generate Token**.
4. Enter a descriptive name (for example, `ci-pipeline` or `terraform-provisioner`).
5. Click **Generate**.
6. Copy the displayed token immediately. It will not be shown again.

### API

```bash
curl -b cookies.txt \
  -X POST http://localhost:8080/api/v1/admin/tokens \
  -H "Content-Type: application/json" \
  -d '{"name": "ci-pipeline"}'
```

Response:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "ci-pipeline",
  "token": "wachd_a3f2c1d4e5b6...",
  "created_at": "2026-04-09T10:00:00Z"
}
```

The `token` field is returned only in this response. The server stores only the SHA-256 hash — the raw value cannot be retrieved after this point. If you lose the token, revoke it and generate a new one.

---

## Using a token

Pass the token in the `Authorization` header on any API request:

```bash
curl -H "Authorization: Bearer wachd_a3f2c1d4e5b6..." \
  http://localhost:8080/api/v1/admin/users
```

Token authentication works on every endpoint that accepts cookie-based sessions, including:

- All `/api/v1/admin/*` endpoints
- All `/api/v1/teams/{teamId}/*` endpoints
- `GET /auth/me`

The `last_used_at` timestamp is updated asynchronously on each authenticated request.

---

## Listing tokens

### Admin panel

Admin → API Tokens shows all your tokens with name, creation date, last used date, and expiry.

### API

```bash
curl -H "Authorization: Bearer wachd_..." \
  http://localhost:8080/api/v1/admin/tokens
```

Response:

```json
{
  "tokens": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "user_id": "...",
      "name": "ci-pipeline",
      "last_used_at": "2026-04-09T09:45:00Z",
      "created_at": "2026-04-01T08:00:00Z"
    }
  ],
  "count": 1
}
```

Each superadmin sees only their own tokens. One superadmin cannot list or revoke another superadmin's tokens.

---

## Revoking a token

### Admin panel

Admin → API Tokens → Revoke (next to the token row).

### API

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X DELETE http://localhost:8080/api/v1/admin/tokens/{tokenId}
```

Returns `204 No Content` on success. The token is invalidated immediately — any in-flight requests using it will complete, but subsequent requests will receive `401 Unauthorized`.

Deleting the owning user also revokes all their tokens automatically (database cascade).

---

## Token scope and permissions

A token carries the same permissions as the superadmin user who created it. It does not carry a reduced scope.

Permission resolution at authentication time:

1. The token hash is looked up in the database.
2. The owning user's `is_active` flag is checked. If the user is deactivated, the token is rejected.
3. The user's team access is resolved fresh via their group memberships (a database query on every request).
4. A synthetic session is built from the resolved data and injected into the request context.

Because team access is resolved on every request, changes to group membership take effect immediately for token-authenticated requests — there is no TTL cache.

### Behaviour differences from cookie sessions

| Behaviour | Cookie session | API token |
|---|---|---|
| `force_password_change` enforcement | Blocked until password changed | Bypassed — pipelines are never interrupted |
| Session expiry | 24 hours (rolling) | No expiry (unless `expires_at` set in DB) |
| Available to SSO users | Yes | No — local superadmin only |

---

## Security model

**Token format:** `wachd_` followed by 64 lowercase hexadecimal characters (32 random bytes from `crypto/rand`). Total length: 70 characters.

**Storage:** Only the SHA-256 hash of the raw token is stored in the database. The raw token cannot be reconstructed from the stored value. If the database is compromised, the stored hashes cannot be reversed to valid tokens.

**Comparison:** Token hash comparisons use `crypto/subtle.ConstantTimeCompare` in the authentication middleware to eliminate timing side channels.

**Transport:** Tokens must only be sent over HTTPS in production. The `Authorization` header is transmitted in plaintext over HTTP — never use tokens over an unencrypted connection.

**Rotation:** Tokens do not expire automatically (the expiry field is reserved for a future feature). Rotate tokens periodically by revoking the existing token and generating a new one. For CI pipelines, consider rotating tokens on a fixed schedule via your secret manager.

**Leak detection:** The `wachd_` prefix is a known-format identifier, allowing secret scanning tools (GitHub secret scanning, truffleHog, GitLeaks) to detect and alert on accidentally committed tokens.

---

## CI/CD examples

### GitHub Actions

Store the token as a repository secret named `WACHD_TOKEN`.

```yaml
- name: Acknowledge incident
  run: |
    curl -X POST \
      -H "Authorization: Bearer ${{ secrets.WACHD_TOKEN }}" \
      -H "Content-Type: application/json" \
      https://wachd.example.com/api/v1/teams/$TEAM_ID/incidents/$INCIDENT_ID/ack
```

### GitLab CI

Store the token as a masked CI/CD variable named `WACHD_TOKEN`.

```yaml
acknowledge:
  script:
    - |
      curl -X POST \
        -H "Authorization: Bearer ${WACHD_TOKEN}" \
        https://wachd.example.com/api/v1/teams/${TEAM_ID}/incidents/${INCIDENT_ID}/ack
```

### Terraform (HTTP provider)

```hcl
provider "http" {}

data "http" "wachd_users" {
  url = "https://wachd.example.com/api/v1/admin/users"

  request_headers = {
    Authorization = "Bearer ${var.wachd_token}"
  }
}
```

### Shell script

```bash
#!/usr/bin/env bash
set -euo pipefail

WACHD_URL="https://wachd.example.com"
TOKEN="${WACHD_TOKEN:?WACHD_TOKEN must be set}"
TEAM_ID="${TEAM_ID:?TEAM_ID must be set}"

# List open incidents
curl -s \
  -H "Authorization: Bearer ${TOKEN}" \
  "${WACHD_URL}/api/v1/teams/${TEAM_ID}/incidents" \
  | jq '.incidents[] | select(.status == "open") | {id, title, severity}'
```

---

## Testing

The PAT system has an automated test suite that must pass before every release.

### Unit tests

No external dependencies required. Run as part of the standard test suite:

```bash
make test
# or
go test -v ./internal/auth/ ./internal/license/
```

### Integration tests

Require a running PostgreSQL and Redis instance (the dev Docker Compose setup satisfies this). These tests exercise the full HTTP request lifecycle against a real database.

```bash
make test-integration
# or
go test -tags integration -v -count=1 ./internal/auth/
```

The integration tests use isolated superadmin accounts created and deleted within each test run. They do not interfere with existing data.

### Pre-release test suite

Run both unit and integration tests together before tagging a release:

```bash
make test-release
```

This runs `go test ./...` followed by `go test -tags integration ./...` and exits non-zero if any test fails.

### What the tests cover

**PAT integration tests** (`internal/auth/pat_integration_test.go`):

| Test | What it asserts |
|---|---|
| `TestPAT_Create_ReturnsTokenWithWachdPrefix` | Created token starts with `wachd_` and has the expected format |
| `TestPAT_Create_EmptyName_Returns400` | Empty token name is rejected with 400 |
| `TestPAT_Create_Unauthenticated_Returns401` | Token creation requires authentication |
| `TestPAT_Bearer_ValidToken_Authenticates` | Valid PAT in `Authorization: Bearer` header authenticates with `auth_type: token` |
| `TestPAT_Bearer_InvalidToken_Returns401` | Unknown token hash is rejected with 401 |
| `TestPAT_Bearer_MalformedHeader_Returns401` | Malformed Authorization headers are rejected |
| `TestPAT_Revoke_TokenBecomesInvalid` | A revoked token returns 401 on subsequent requests |
| `TestPAT_List_DoesNotExposeRawToken` | List endpoint never returns the raw token value |
| `TestPAT_StoredHashNotPlaintext` | Database stores a 64-character SHA-256 hex hash, not the raw token |

**License unit tests** (`internal/license/license_test.go`):

| Test | What it asserts |
|---|---|
| `TestOSS_Defaults` | OSS tier returns correct constants and `IsPaid()` returns false |
| `TestOSS_HardcodedLimits` | Regression guard — OSS limits are exactly 1 team, 5 users, 1 000 alerts |
| `TestLoad_EmptyKey_ReturnsOSS` | Empty or whitespace key loads OSS with no error |
| `TestLoad_MalformedKey_ReturnsOSSWithError` | Malformed JWT falls back to OSS and returns an error |
| `TestLoad_ValidSMBKey` | Valid signed SMB key loads correct tier and limits |
| `TestLoad_ValidEnterpriseKey` | Valid signed Enterprise key loads correct tier |
| `TestLoad_TamperedSignature_ReturnsOSSWithError` | Modified signature falls back to OSS |
| `TestLoad_WrongPublicKey_ReturnsOSSWithError` | Key signed by a different keypair is rejected |
| `TestLoad_TamperedPayload_ReturnsOSSWithError` | Modified payload (without re-signing) is rejected |
| `TestLoad_ExpiredKey_BeyondGrace_ReturnsOSSWithError` | Key expired more than 7 days ago falls back to OSS |
| `TestLoad_ExpiredKey_WithinGrace_ReturnsPaidWithFlag` | Key expired within 7-day grace period loads paid tier with `IsGracePeriod: true` |
| `TestLoad_InvalidIssuer_ReturnsOSSWithError` | Wrong `iss` claim is rejected |
| `TestLoad_UnknownTier_ReturnsOSSWithError` | Unrecognised tier string falls back to OSS |
| `TestLoad_ZeroLimits_ReturnsOSSWithError` | Zero or negative limits in payload are rejected |

The license tests generate a fresh Ed25519 keypair at runtime — no production private key is stored in the repository.
