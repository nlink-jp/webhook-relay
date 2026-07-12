# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.2.0] - 2026-07-12

### Removed

- **darwin/amd64 (Intel) pre-built binary.** The macOS local-dev binaries
  now ship **arm64 only**, per the org-wide policy (darwin is Apple-Silicon
  only; no universal binaries). Intel Mac users can build from source. The
  primary Cloud Run deployment target (linux/amd64) is unaffected.

### Changed

- **Linux release archives are now `.tar.gz`** (darwin/windows remain `.zip`),
  per `nlink-jp/.github` CONVENTIONS.md §Release Archive Standard. Archives
  still bundle `LICENSE` + `README.md` alongside the canonical binary.
- **darwin code-signature identifier** is now the canonical `webhook-relay`
  (was `webhook-relay-darwin-arm64`), set via `codesign -i` so it stays stable
  after the archived binary is renamed to its canonical name.

No change to the binary's behaviour — a packaging / build-config release.

## [0.1.1] - 2026-05-23

### Added

- **`package` Makefile target.** Builds all 5 platforms, signs darwin
  binaries with Developer ID, zips each with LICENSE + README.md
  using versioned naming
  (`webhook-relay-vX.Y.Z-<os>-<arch>.zip`), and notarizes the
  darwin zips. Replaces the manual zip step that produced the
  v0.1.0 release.

### Changed

- **Darwin releases are now Developer ID signed and Apple-notarized.**
  `webhook-relay-v0.1.1-darwin-{amd64,arm64}.zip` carry full Apple
  Developer ID Application signatures and notarization tickets
  from Apple. Darwin binaries are local-development targets (the
  primary deployment is Cloud Run linux/amd64); the signing fix
  matters mainly for developers running `webhook-relay` locally
  under Dropbox-synced (FileProvider-managed) paths, where macOS
  was killing ad-hoc-signed binaries with `com.apple.provenance`
  set. Pipeline: `scripts/codesign-darwin.sh` +
  `scripts/notarize-darwin.sh`, driven by `make package`. Adopts
  the org-wide convention in `nlink-jp/.github` CONVENTIONS.md
  §Code Signing.

No behaviour change to the binary itself — feature-wise this is
identical to v0.1.0.

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
