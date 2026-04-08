# CLAUDE.md — webhook-relay

## Project overview

Authenticated webhook receiver that writes payloads to GCS.
Internet-facing ingestion gateway with VPC network isolation.

## Build & test

```bash
make build       # Build → dist/webhook-relay
make test        # Run tests
go test ./...    # Same without Makefile
```

## Architecture

```
internal/
├── auth/          # API Key authentication (constant-time)
├── backend/       # Backend interface
│   └── gcs/       # GCS implementation
├── config/        # Environment variable config
├── middleware/     # Security, rate limit, logging, body size
└── server/        # HTTP server, routing, handler
```

## Key conventions

- Security-first: all input validated, all requests logged
- API key via X-API-Key header, stored in Secret Manager
- VPC isolation: egress only to Google APIs
- Service account has write-only GCS access (no read/delete)
- Non-root container
