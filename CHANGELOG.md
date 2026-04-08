# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.1.0] - 2026-04-09

### Added

- HTTP server with pluggable backend architecture
- GCS backend for writing payloads to Cloud Storage
- API Key authentication with constant-time comparison
- Per-IP rate limiting (token bucket via golang.org/x/time/rate)
- Request body size limit (configurable, default 25 MB)
- Path traversal prevention with file extension whitelist
- Structured JSON audit logging for every request
- Security response headers (X-Content-Type-Options, X-Frame-Options, Cache-Control)
- Health check endpoint (/healthz) without authentication
- VPC-isolated deployment with Private Google Access
- Non-root Docker container with multi-stage build
- One-liner deploy script with VPC, IAM, and Secret Manager setup
- Security design document with threat model (docs/security.md)
