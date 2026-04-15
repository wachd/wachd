# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 0.1.x   | Yes       |

---

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

To report a security issue, email **info@ntcdev.com** with:

- A description of the vulnerability
- Steps to reproduce
- The potential impact
- Any suggested mitigations (optional)

You will receive an acknowledgement within 48 hours. We aim to release a fix within 14 days of confirmation for critical issues.

Once a fix is released, we will publish a security advisory on the GitHub repository.

---

## Scope

The following are in scope:

- Webhook authentication bypass
- Multi-tenant data isolation failures (one team accessing another team's data)
- SQL injection
- Authentication or authorization bypass in the REST API
- PII leakage to logs or external services
- Secrets exposed in API responses or logs

---

## Out of Scope

- Vulnerabilities in third-party dependencies (report those upstream)
- Issues requiring physical access to the deployment host
- Social engineering attacks

---

## Security Design Principles

- All database queries use parameterized statements (`$1`, `$2`)
- Webhook endpoints validate an HMAC secret before processing
- Every API route scopes data to the authenticated team
- PII is stripped from all data before it reaches the analysis backend
- No external API calls are made with raw log or alert data
- Secrets are loaded from environment variables or Kubernetes Secrets — never committed to source code
