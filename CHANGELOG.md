# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.3.0] - 2026-11-09

### Security

Findings from the organisation's code-audit run against this repository
(quick profile). Each entry names the finding's fingerprint.

- **The per-IP rate limiter keyed on the client-supplied leftmost
  `X-Forwarded-For` entry.** Cloud Run appends the observed client IP on the
  right and passes the rest through, so an anonymous caller who rotated the
  header got a fresh token bucket on every request, and each value became a
  map entry that nothing capped. The key is now the rightmost entry, used
  only when it parses as an IP, with `RemoteAddr` as the fallback; IPv6
  clients are keyed by /64. The visitor map holds at most 10,000 entries
  (least recently seen is evicted), and `http.Server` sets
  `MaxHeaderBytes` to 64 KiB. The test that pinned the leftmost choice now
  asserts the opposite.
  `internal/middleware/ratelimit.go:extractIP:client-controlled-leftmost-xff-rate-limit-key`
- **Service-account roles are bound on the resources, not the project.**
  `deploy.sh` granted `roles/storage.objectCreator` and
  `roles/secretmanager.secretAccessor` project-wide, so the internet-facing
  service identity could write to every bucket and read every secret in the
  project, against what docs/en/security.md claimed. The bucket role is now
  bound on `gs://${GCS_BUCKET}` and the secret role on
  `webhook-relay-api-key`, each once the resource exists; only
  `roles/logging.logWriter` stays at project scope. Re-running the script
  does not remove the older project-level bindings: README.md "Upgrading an
  existing deployment" gives the two `remove-iam-policy-binding` commands.
  `deploy/deploy.sh:ROLES:project-scoped-sa-bindings`
- **Release binaries no longer embed the builder's home-directory paths.**
  `make build` and `make build-all` ran `go build` without `-trimpath`, so
  every published binary (GitHub release assets, the Homebrew install)
  recorded the release machine's absolute source and module-cache paths
  (`/Users/<login>/...`), readable with `strings` or `go version -m` and
  printed in panic traces. Both targets, and the container build in
  `deploy/Dockerfile`, now pass `-trimpath`. `make verify-release` extracts
  the binary from every archive and fails if it contains `/Users/` or
  `/home/`, so a `GOFLAGS` override or a hand-run build cannot bring the
  paths back. Assets already published keep them.
  `Makefile:build-all:release-binaries-built-without-trimpath`
- **Linux release archives no longer record the builder's login and group
  names.** `make package` called `tar` with no ownership override, so each
  member header of the published `.tar.gz` carried the maintainer's macOS
  account (`uname`/`gname`) and uid/gid, visible in any `tar -tv` listing.
  The archives are now written with `--uid 0 --gid 0 --uname root --gname
  root`, and the darwin/windows zips with `zip -X`, which drops the Unix
  extra field holding uid/gid. `make verify-release` reads every Linux
  member's owner fields with Python's `tarfile` and fails unless all are
  `0:0 root:root`. Archives already published keep the names.
  `Makefile:package:linux-tarball-records-builder-owner-names`
- **A failed upload no longer leaves a truncated or empty object in the
  bucket.** The audit left two questions open about the pinned
  `cloud.google.com/go/storage` (v1.62.0); both are now settled from its
  source (`writer.go`, `http_client.go`) and reproduced against a fake
  upload endpoint in `internal/backend/gcs/gcs_test.go`:
  `Writer.Close` closes the upload pipe without an error, so the upload
  goroutine finalises whatever bytes were written so far; and `Close` on a
  writer that received no `Write` opens the upload first and commits a
  zero-length object. `Backend.Write` therefore committed a truncated object
  while answering 500 whenever `io.Copy` failed part-way (body over
  `WEBHOOK_RELAY_MAX_REQUEST_BYTES`, read timeout, client gone). It now
  derives a cancellable context for the writer and cancels it on a copy
  error, which is the library's documented abort: nothing reaches the
  bucket. The write log records the bytes actually copied, not the declared
  `Content-Length`.
  `internal/backend/gcs/gcs.go:Backend.Write:close-commits-partial-object-on-read-error`
- **Oversized and empty bodies are refused before the upload starts.** The
  second open question from the audit is settled the same way: a request
  with no `Content-Length` (chunked) and no body passed the
  `ContentLength == 0` check and would have ended as a zero-length object
  with a 201. The handler now reads the first byte before opening a backend
  writer and answers 400 when there is none. A body declared larger than
  `WEBHOOK_RELAY_MAX_REQUEST_BYTES` is answered 413 before the backend is
  touched, and a chunked body that outgrows the limit while streaming is
  also answered 413 (the `http.MaxBytesError` was mapped to 500 before).
  `internal/server/server.go:handleIngest:empty-chunked-body-bypasses-contentlength-check`
- **Every route is audit logged and carries the security headers.**
  `Handler()` returned the bare mux, so only `/ingest/` had `AuditLog` and
  `SecurityHeaders`; `/healthz`, unknown paths (the mux's 404) and its
  redirects left no structured record, against the "every request is
  logged" statement in docs/en/security.md. Both middlewares now wrap the
  whole mux in `Handler()` and are no longer repeated per route, so a probe
  of `/.env` shows up in the audit trail with its 404.
  `internal/server/server.go:New:audit-log-wraps-only-ingest-route`
- **Unparseable or unusable configuration values no longer fall back to
  defaults.** `intEnv` and `floatEnv` dropped the `strconv` error, so a typo
  in `WEBHOOK_RELAY_MAX_REQUEST_BYTES`, `WEBHOOK_RELAY_RATE_LIMIT_RPS` or
  `WEBHOOK_RELAY_RATE_LIMIT_BURST` silently ran the service on the documented
  defaults rather than the operator's tighter values. `Load` now returns an
  error naming the key for a present value that does not parse, is
  non-positive, or is not finite (`strconv.ParseFloat` accepts `inf`, and
  `rate.Limit(+Inf)` would have switched the limiter off); `PORT` must be in
  1–65535. A present `WEBHOOK_RELAY_ALLOWED_EXTENSIONS` with no entries
  (`""`, `,`) is also an error, where before `,` turned the extension check
  off without a word. The test asserting the fallback is replaced by one
  asserting the error for every numeric key.
  `internal/config/config.go:intEnv:parse-errors-silently-fall-back-to-default`

### Fixed

- **`deploy/deploy.env` is gitignored, as the template and README already
  claimed.** `.gitignore` matched only the exact name `.env`, so the file
  operators are told to create (project ID, region, bucket name) was one
  `git add -A` away from a public commit.
  `repo:gitignore:deploy-env-not-ignored`
- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).
- **The Linux archives no longer carry macOS file metadata.** macOS `tar` wrote
  each bundled file's extended attributes (`com.apple.provenance`, and a Dropbox
  attribute where the tree is synced) into the `.tar.gz` twice: as AppleDouble
  `._` members, which GNU tar extracts as stray `._<name>` files beside the real
  ones, and as `LIBARCHIVE.xattr.*` / `SCHILY.xattr.*` pax headers, which it
  reports as unknown keywords. `make package` now archives with
  `COPYFILE_DISABLE=1 tar --no-xattrs`; each setting stops one of the two.
  Archives already published still carry them; the files themselves are
  unaffected.

### Internal

- `make verify-release` also judges each Linux archive: no AppleDouble or other
  macOS metadata members — listed with `--options 'tar:!mac-ext'`, because a
  plain macOS listing folds `._` members away — no extended attributes as pax
  headers, owner fields all `0:0 root:root`, and exactly the canonical binary,
  `README.md` and `LICENSE`, compared in the C locale.
- The Linux-archive check in `make verify-release` reads each archive's pax
  headers with Python's `tarfile` instead of grepping the decompressed stream,
  which also matched file text that names the keywords (a bundled CHANGELOG,
  for one).

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
