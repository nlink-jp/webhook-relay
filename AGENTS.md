# AGENTS.md — webhook-relay

## Summary

webhook-relay is an authenticated HTTP webhook receiver deployed as a
Cloud Run Service. It receives payloads via POST and writes them to a
GCS bucket. Designed for internet-facing use with VPC network isolation.

## Build & test commands

```bash
make build       # Build → dist/webhook-relay
make build-all   # Cross-compile all platforms
make test        # Run tests
make clean       # Remove dist/
```

## Key directory structure

```
webhook-relay/
├── main.go                    # Entry point
├── cmd/
│   └── root.go                # Server startup, shutdown
├── internal/
│   ├── auth/apikey.go         # API Key auth (constant-time)
│   ├── backend/
│   │   ├── backend.go         # Backend interface
│   │   └── gcs/gcs.go         # GCS backend
│   ├── config/config.go       # Env var configuration
│   ├── middleware/
│   │   ├── security.go        # Headers, body limit, path validation
│   │   ├── ratelimit.go       # Per-IP rate limiting
│   │   └── logging.go         # Structured audit logging
│   └── server/server.go       # HTTP server, routing
├── deploy/
│   ├── Dockerfile             # Multi-stage, non-root
│   ├── deploy.sh              # VPC + Cloud Run setup
│   └── deploy.env.template
├── docs/
│   └── security.md            # Threat model, controls
└── Makefile
```

## Module path

`github.com/nlink-jp/webhook-relay`

## Environment variables

| Variable | Description |
|----------|-------------|
| `WEBHOOK_RELAY_API_KEY` | API key (required, from Secret Manager) |
| `WEBHOOK_RELAY_GCS_BUCKET` | Target GCS bucket |
| `WEBHOOK_RELAY_GCS_PROJECT` | GCP project ID |
| `WEBHOOK_RELAY_RATE_LIMIT_RPS` | Rate limit per IP (default: 10) |
| `WEBHOOK_RELAY_RATE_LIMIT_BURST` | Burst size (default: 20) |
| `WEBHOOK_RELAY_MAX_REQUEST_BYTES` | Max body size (default: 25 MB) |
| `WEBHOOK_RELAY_ALLOWED_EXTENSIONS` | Whitelist (default: .eml,.msg) |
| `PORT` | Listen port (default: 8080) |

## Gotchas

- Internet-facing service — all changes must consider security impact
- API key comparison is constant-time (crypto/subtle) — do not change to ==
- VPC egress goes through Private Google Access only — no internet egress
- Service account is write-only (objectCreator) — cannot read or delete GCS objects
