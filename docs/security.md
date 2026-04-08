# Security Design — webhook-relay

This document describes the security architecture, threat model, and controls
implemented in webhook-relay. It is intended for security review and
production deployment approval.

---

## Table of Contents

1. [System Overview](#system-overview)
2. [Threat Model](#threat-model)
3. [Security Controls](#security-controls)
4. [Network Architecture](#network-architecture)
5. [Authentication & Authorization](#authentication--authorization)
6. [Input Validation](#input-validation)
7. [Rate Limiting](#rate-limiting)
8. [Audit Logging](#audit-logging)
9. [Credential Management](#credential-management)
10. [Container Security](#container-security)
11. [IAM Principle of Least Privilege](#iam-principle-of-least-privilege)
12. [Residual Risks](#residual-risks)

---

## System Overview

webhook-relay is a stateless HTTP service that receives webhook payloads
from external sources (e.g., Power Automate) and writes them to a GCS bucket.
It is deployed as a Google Cloud Run Service with VPC network isolation.

**Data flow:**
```
External Source (Power Automate)
    │
    │ HTTPS POST /ingest/gcs/{path}
    │ Header: X-API-Key
    │ Body: email file (binary)
    │
    ▼
┌────────────────────────────────┐
│ Cloud Run Service              │
│ (webhook-relay)                │
│                                │
│ Internet ingress (HTTPS/TLS)   │
│     ↓                          │
│ Middleware chain:              │
│   1. Audit log                 │
│   2. Security headers          │
│   3. Request size limit        │
│   4. Per-IP rate limit         │
│   5. API key authentication    │
│   6. HTTP method enforcement   │
│     ↓                          │
│ Path validation                │
│     ↓                          │
│ GCS write (via VPC egress)     │
│                                │
│ VPC Direct Egress ─────────────┼──┐
└────────────────────────────────┘  │
                                    │
┌───────────────────────────────────┴──┐
│ VPC: webhook-relay-vpc               │
│ ┌──────────────────────────────────┐ │
│ │ Subnet: 10.100.0.0/28           │ │
│ │ Private Google Access: ON       │ │
│ │                                  │ │
│ │ Egress → Google APIs only       │ │
│ │ (no internet egress)            │ │
│ └──────────────────────────────────┘ │
│                                      │
│ Firewall: deny-all-ingress          │
└──────────────────────────────────────┘
```

**The service does NOT:**
- Store any data locally (stateless)
- Read from GCS (write-only)
- Execute or interpret received files
- Maintain sessions or cookies
- Expose admin interfaces

---

## Threat Model

### Assets

| Asset | Sensitivity | Location |
|-------|-------------|----------|
| API key | High | GCP Secret Manager → injected as env var |
| Email files (in transit) | Medium | TLS-encrypted HTTPS body |
| Email files (at rest) | Medium | GCS bucket (server-side encryption) |
| Audit logs | Medium | Cloud Logging (structured JSON) |

### Threat Actors

| Actor | Capability | Motivation |
|-------|-----------|------------|
| External attacker | Internet access, automated scanning | Data exfiltration, DoS, lateral movement |
| Compromised Power Automate account | Valid API key, ability to send requests | Unauthorized data injection |
| Insider (cloud admin) | GCP project access | Policy violation |

### Threats and Mitigations

| ID | Threat | Impact | Control | Residual Risk |
|----|--------|--------|---------|---------------|
| T1 | Brute-force API key | Unauthorized access | Constant-time comparison, rate limiting, 256-bit key | Low |
| T2 | DoS via large payloads | Service disruption | 25 MB request size limit, Cloud Run auto-scaling cap | Low |
| T3 | DoS via request flood | Service disruption, cost | Per-IP rate limiting (10 rps), max instances cap | Low |
| T4 | Path traversal | Write to unintended locations | Path validation (no `..`, extension whitelist) | Low |
| T5 | Data exfiltration via egress | Sensitive data leak | VPC with no internet egress, Private Google Access only | Low |
| T6 | API key leak | Unauthorized access | Secret Manager, no logging of key values, key rotation | Medium |
| T7 | Malicious file upload | Downstream system compromise | Extension whitelist, size limit. Files are not executed. | Medium (depends on downstream) |
| T8 | TLS downgrade | Eavesdropping | Cloud Run enforces HTTPS, HSTS | Low |
| T9 | Log injection | Log tampering | Structured JSON logs, no user input in log keys | Low |

---

## Security Controls

### Control Summary

| Category | Control | Implementation |
|----------|---------|----------------|
| Authentication | API key (X-API-Key header) | `internal/auth/apikey.go` |
| Authorization | Backend + path validation | `internal/server/server.go` |
| Input validation | Path traversal guard | `internal/middleware/security.go` |
| Input validation | File extension whitelist | `internal/middleware/security.go` |
| Input validation | Request size limit | `internal/middleware/security.go` |
| Rate limiting | Per-IP token bucket | `internal/middleware/ratelimit.go` |
| Audit | Structured JSON request log | `internal/middleware/logging.go` |
| Network | VPC isolation + Private Google Access | `deploy/deploy.sh` |
| Network | Deny-all ingress firewall | `deploy/deploy.sh` |
| Transport | TLS 1.2+ (enforced by Cloud Run) | Cloud Run platform |
| Container | Non-root user, minimal base image | `deploy/Dockerfile` |
| Secrets | GCP Secret Manager | `deploy/deploy.sh` |
| IAM | Least privilege (write-only GCS) | `deploy/deploy.sh` |

---

## Network Architecture

### VPC Design

```
┌─────────────────────────────────────────────┐
│ webhook-relay-vpc                           │
│                                             │
│  Subnet: webhook-relay-subnet               │
│  CIDR:   10.100.0.0/28 (16 IPs)            │
│  Private Google Access: ENABLED             │
│                                             │
│  Firewall Rules:                            │
│    webhook-relay-vpc-deny-all-ingress       │
│    Direction: INGRESS                       │
│    Action: DENY ALL                         │
│    Source: 0.0.0.0/0                        │
│    Priority: 65534                          │
│                                             │
│  Egress: Google APIs only                   │
│    (via Private Google Access)              │
│    No internet egress possible              │
└─────────────────────────────────────────────┘
```

### Why VPC Isolation?

1. **No internet egress:** Even if the application is compromised, it cannot
   make outbound connections to arbitrary internet hosts. The only reachable
   destinations are Google APIs (GCS, Secret Manager, Cloud Logging) via
   Private Google Access.

2. **Deny-all ingress firewall:** The VPC firewall blocks all ingress traffic
   to the subnet. Cloud Run handles its own ingress independently of VPC
   firewalls, so the webhook endpoint remains reachable, but no other
   resources in the VPC can be accessed.

3. **Minimal subnet:** /28 CIDR (16 IPs) minimizes the blast radius. No
   other services share this network.

### Cloud Run Ingress

Cloud Run ingress is set to `all` because the service must receive traffic
from Power Automate (Microsoft-owned IP ranges that are not predictable).
Authentication is handled at the application layer (API key), not at the
network layer.

---

## Authentication & Authorization

### API Key Authentication

- **Mechanism:** `X-API-Key` header checked against a stored secret
- **Comparison:** `crypto/subtle.ConstantTimeCompare` (timing-safe)
- **Key storage:** GCP Secret Manager, injected as environment variable
- **Key generation:** `openssl rand -hex 32` (256-bit entropy)
- **Key rotation:** Create a new secret version, update Cloud Run, revoke old version

### Why API Key (not OAuth2 / JWT)?

Power Automate's HTTP connector supports custom headers but has limited
OAuth2 capabilities (especially without Entra ID app registration). An API
key in a custom header is the most compatible authentication mechanism.

### Constant-Time Comparison

Standard string comparison (`==`) leaks information via timing differences:
an attacker can determine how many bytes of the key are correct by measuring
response latency. `crypto/subtle.ConstantTimeCompare` takes constant time
regardless of which byte differs, preventing timing side-channel attacks.

---

## Input Validation

### Path Validation

All object paths are validated before reaching any backend:

| Check | Rationale |
|-------|-----------|
| Non-empty | Prevent writing to bucket root |
| No `..` or `.` segments | Prevent directory traversal |
| `path.Clean()` consistency | Detect encoded traversal (`//`, trailing `/`) |
| Extension whitelist | Only `.eml` and `.msg` by default |

### Request Size Limit

- Default: 25 MB
- Enforced via `http.MaxBytesReader` which returns 413 if exceeded
- Prevents memory exhaustion and storage abuse

### HTTP Method Enforcement

- Only `POST` is accepted on `/ingest/` routes
- All other methods return 405 Method Not Allowed
- `GET /healthz` is the only non-POST endpoint (unauthenticated, returns `{"status":"ok"}`)

---

## Rate Limiting

### Algorithm

Per-IP token bucket rate limiter using `golang.org/x/time/rate`:

| Parameter | Default | Configurable |
|-----------|---------|:---:|
| Rate | 10 requests/second | Yes (`WEBHOOK_RELAY_RATE_LIMIT_RPS`) |
| Burst | 20 requests | Yes (`WEBHOOK_RELAY_RATE_LIMIT_BURST`) |
| Response | 429 + `Retry-After: 1` | — |

### IP Extraction

1. `X-Forwarded-For` header (set by Cloud Run, trusted)
2. Leftmost IP taken (original client)
3. Falls back to `RemoteAddr` if header absent

### Stale Entry Cleanup

Visitor entries are evicted after 3 minutes of inactivity to prevent
memory growth from scanning traffic.

---

## Audit Logging

Every request is logged as structured JSON to stdout, which Cloud Run
forwards to Cloud Logging:

```json
{
  "timestamp": "2026-04-09T10:00:00Z",
  "method": "POST",
  "path": "/ingest/gcs/inbox/alert.eml",
  "remote_addr": "10.0.0.1:1234",
  "x_forwarded_for": "203.0.113.5",
  "status": 201,
  "duration_ms": 45,
  "bytes_in": 102400,
  "user_agent": "PowerAutomate/1.0"
}
```

### What is NOT logged

- API key values (neither valid nor invalid)
- Request body content
- Response body content
- Internal error details (returned as generic "backend write failed")

---

## Credential Management

| Credential | Storage | Access Method | Rotation |
|-----------|---------|---------------|----------|
| API key | Secret Manager | Cloud Run secret mount (env var) | Add new version → redeploy → delete old version |
| GCS access | Service account (ADC) | IAM binding (no key file) | Managed by GCP (no manual rotation) |

### No Key Files

The service uses Application Default Credentials (ADC) on Cloud Run,
which resolves to the attached service account. No JSON key files are
created, stored, or deployed.

---

## Container Security

| Measure | Implementation |
|---------|----------------|
| Multi-stage build | Build in `golang:1.24-alpine`, run in `alpine:3.21` |
| Non-root user | `adduser -D -H appuser` + `USER appuser` |
| Minimal image | Alpine base, only `ca-certificates` installed |
| Static binary | `CGO_ENABLED=0`, no dynamic linking |
| No shell needed | Entrypoint is the binary directly |

---

## IAM Principle of Least Privilege

The service account has the minimum permissions required:

| Role | Justification |
|------|---------------|
| `roles/storage.objectCreator` | Write objects to GCS. **Not** `objectAdmin` — cannot read, list, or delete existing objects. |
| `roles/secretmanager.secretAccessor` | Read API key from Secret Manager |
| `roles/logging.logWriter` | Write structured logs to Cloud Logging |

**Notable exclusions:**
- No `storage.objectViewer` — the service cannot read existing objects
- No `storage.objectAdmin` — the service cannot delete objects
- No `run.invoker` — not needed (the service handles its own auth)
- No `iam.serviceAccountUser` — cannot impersonate other accounts

---

## Residual Risks

| Risk | Severity | Mitigation Status | Notes |
|------|----------|-------------------|-------|
| API key compromise via Power Automate admin | Medium | Accepted | Key can be rotated immediately. Audit logs enable detection. |
| Malicious file content (e.g., crafted .eml with exploit) | Medium | Partial | webhook-relay does not parse or execute files. Downstream consumers (mail-triage) must validate. |
| Cloud Run cold start delays (1-2s) | Low | Accepted | Webhook callers (Power Automate) tolerate this latency. |
| GCS bucket misconfiguration (public access) | Medium | Mitigated | Uniform bucket-level access enforced. No public access by default. |
| Rate limiter bypass via distributed IPs | Low | Accepted | Cloud Run max-instances cap limits overall cost. Per-IP rate limiting handles single-source abuse. |
