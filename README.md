# webhook-relay

Authenticated webhook receiver that writes payloads to GCS. Designed as an
internet-facing ingestion gateway with security-first architecture.

Deployed as a Google Cloud Run Service with VPC network isolation,
scale-to-zero billing, and per-IP rate limiting.

## Features

- **API Key authentication** with constant-time comparison (timing-attack safe)
- **Per-IP rate limiting** (token bucket, configurable RPS and burst)
- **Request size limit** (default 25 MB)
- **Path traversal prevention** with file extension whitelist
- **VPC network isolation** — egress only to Google APIs via Private Google Access
- **Structured audit logging** (JSON to Cloud Logging)
- **Non-root container** with multi-stage build
- **Pluggable backend interface** — GCS in v0.1.0, extensible to Pub/Sub, HTTP, etc.
- **Scale-to-zero** — no cost when idle

## Usage

```bash
# Upload a file via webhook
curl -X POST "https://SERVICE_URL/ingest/gcs/inbox/alert.eml" \
  -H "X-API-Key: YOUR_API_KEY" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @alert.eml

# Health check (no auth required)
curl "https://SERVICE_URL/healthz"
```

### API

```
POST /ingest/{backend}/{path...}
  Headers:
    X-API-Key: <secret>         (required)
    Content-Type: <any>         (passthrough)
  Body: raw file content
  Response: 201 Created

GET /healthz
  Response: 200 {"status":"ok"}
```

### Power Automate Configuration

In your Power Automate flow, add an **HTTP** action:

| Field | Value |
|-------|-------|
| Method | POST |
| URI | `https://SERVICE_URL/ingest/gcs/inbox/{{triggerOutputs()?['body/subject']}}.eml` |
| Headers | `X-API-Key`: your API key |
| Body | Email MIME content |

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `WEBHOOK_RELAY_API_KEY` | (required) | API key for authentication |
| `WEBHOOK_RELAY_GCS_BUCKET` | (required) | Target GCS bucket |
| `WEBHOOK_RELAY_GCS_PROJECT` | | GCP project ID |
| `WEBHOOK_RELAY_RATE_LIMIT_RPS` | `10` | Requests per second per IP |
| `WEBHOOK_RELAY_RATE_LIMIT_BURST` | `20` | Burst size per IP |
| `WEBHOOK_RELAY_MAX_REQUEST_BYTES` | `26214400` | Max request body (25 MB) |
| `WEBHOOK_RELAY_ALLOWED_EXTENSIONS` | `.eml,.msg` | Allowed file extensions |
| `PORT` | `8080` | Server listen port |

## Deployment

```bash
cp deploy/deploy.env.template deploy/deploy.env
# Edit deploy/deploy.env

./deploy/deploy.sh deploy/deploy.env

# Generate and store API key
openssl rand -hex 32 | \
  gcloud secrets versions add webhook-relay-api-key \
    --data-file=- --project=PROJECT_ID
```

## Building

```bash
make build      # Build for current platform → dist/
make build-all  # Cross-compile all platforms
make test       # Run tests
make clean      # Remove dist/
```

## Documentation

- [Security Design](docs/security.md) — Threat model, controls, network architecture
- [README.ja.md](README.ja.md) (Japanese)
- [CHANGELOG.md](CHANGELOG.md)
