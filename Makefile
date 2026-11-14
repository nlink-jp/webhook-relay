BINARY  := webhook-relay
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
# -trimpath records module-relative file names instead of the builder's
# absolute paths (/Users/<login>/...) in the binary's build info and
# pclntab; verify-release refuses an archive that carries them.
GOBUILD := go build -trimpath $(LDFLAGS)
DIST_DIR := dist

# macOS Developer ID signing / notarization (see nlink-jp/.github
# CONVENTIONS.md §Code Signing). Defaults match any Developer ID
# Application cert in the keychain and the org-standard notary
# profile. Builds without these fall back to ad-hoc / un-notarized
# with a one-line warning — see scripts/codesign-darwin.sh.
# Note: webhook-relay's primary deployment target is Cloud Run
# (linux/amd64); macOS signing applies to local-dev binaries.
CODESIGN_IDENTITY ?= Developer ID Application
NOTARY_PROFILE    ?= nlink-jp-notary

# darwin ships arm64 only (no amd64, no universal). linux/windows keep their
# matrix (Cloud Run deploys linux/amd64).
PLATFORMS := darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build build-all package verify-release test clean vuln

build:
	@mkdir -p $(DIST_DIR)
	$(GOBUILD) -o $(DIST_DIR)/$(BINARY) .
	@scripts/codesign-darwin.sh $(DIST_DIR)/$(BINARY) "$(CODESIGN_IDENTITY)"

build-all:
	@mkdir -p $(DIST_DIR)
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = windows ] && ext=".exe"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GOBUILD) -o $(DIST_DIR)/$(BINARY)-$$os-$$arch$$ext . ; \
	done
	@scripts/codesign-darwin.sh $(DIST_DIR)/$(BINARY)-darwin-arm64 "$(CODESIGN_IDENTITY)" "$(BINARY)"

## vuln: known vulnerabilities in the modules and the standard library each
## release build uses (govulncheck) — once per platform in PLATFORMS, with the
## environment build-all builds it with, because an advisory in an OS-only
## package shows only in that OS's build. `package` depends on it, so a release
## cannot be built past a finding. It needs the network, and its database
## changes under an unchanged tree: a finding can appear without a commit.
vuln:
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		echo "govulncheck $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch govulncheck ./... || exit 1; \
	done

## package: Build all platforms, archive with version suffix (zip for
## darwin/windows, tar.gz for linux), bundle the canonical binary +
## README.md + LICENSE, and notarize the darwin build → dist/. Asset
## naming follows the org Release Archive Standard
## (webhook-relay-vX.Y.Z-<os>-<arch>.<ext>). Archive members record no
## builder identity: tar writes uid/gid 0 and root/root, zip -X drops the
## Unix extra field that holds uid/gid.
package: vuln build-all
	@cd $(DIST_DIR) && for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = windows ] && ext=".exe"; \
		stage=_pkg; rm -rf $$stage; mkdir -p $$stage; \
		cp "$(BINARY)-$$os-$$arch$$ext" "$$stage/$(BINARY)$$ext"; \
		cp ../README.md ../LICENSE $$stage/; \
		base="$(BINARY)-$(VERSION)-$$os-$$arch"; \
		if [ "$$os" = linux ]; then ( cd $$stage && COPYFILE_DISABLE=1 tar --no-xattrs --uid 0 --gid 0 --uname root --gname root -czf "../$$base.tar.gz" * ); \
		else ( cd $$stage && zip -q -X "../$$base.zip" * ); fi; \
		rm -rf $$stage; \
	done
	@scripts/notarize-darwin.sh $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip "$(NOTARY_PROFILE)"

## verify-release: refuse to release a zip that is un-notarized, stale, does
## not unpack, does not run, or holds a build from another tag, a linux
## archive that carries macOS metadata or anything but its canonical files,
## and any archive whose binary embeds the builder's home-directory paths.
## Every step fails closed; only the spctl line is informational.
verify-release:
	@test -f "$(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip.notarized" || { \
		echo "verify-release: FAIL — $(BINARY)-$(VERSION)-darwin-arm64.zip has no notarization marker."; \
		echo "  make package must end with '[notarize] ...: Accepted'. Do not upload this zip."; \
		exit 1; }
	@test "$(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip.notarized" -nt "$(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip" || { \
		echo "verify-release: FAIL — the zip was rebuilt after its marker (re-run make package)."; \
		exit 1; }
	@tmp=$$(mktemp -d); rc=0; \
		if ! unzip -oq "$(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip" -d "$$tmp"; then \
			echo "verify-release: FAIL — the zip does not unpack. Do not upload it."; rc=1; \
		elif ! out=$$("$$tmp/$(BINARY)" --version 2>&1); then \
			echo "verify-release: FAIL — the packaged binary does not run:"; \
			echo "  $$out"; rc=1; \
		elif ! printf '%s\n' "$$out" | grep -qF "$(VERSION)"; then \
			echo "verify-release: FAIL — the packaged binary reports \"$$out\", not $(VERSION)."; \
			echo "  The zip holds a build from another tag (re-run make package)."; rc=1; \
		else \
			echo "  $$out"; \
			spctl -a -vv -t install "$$tmp/$(BINARY)" 2>&1 | head -2 || true; \
		fi; \
		rm -rf "$$tmp"; \
		exit $$rc
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		[ "$$os" = linux ] || continue; \
		f="$(DIST_DIR)/$(BINARY)-$(VERSION)-$$os-$$arch.tar.gz"; \
		names=$$(tar --options 'tar:!mac-ext' -tzf "$$f") || { echo "verify-release: FAIL — $$f does not list."; exit 1; }; \
		if printf '%s\n' "$$names" | grep -qE '(^|/)(\._|PaxHeader|__MACOSX)'; then \
			echo "verify-release: FAIL — $$f carries macOS metadata entries."; \
			echo "  macOS tar writes ._ members unless COPYFILE_DISABLE=1 is set, and lists them only with !mac-ext."; \
			exit 1; fi; \
		xh=$$(python3 -c 'import sys, tarfile; print(" ".join(m.name for m in tarfile.open(sys.argv[1]) if any(k.startswith(("LIBARCHIVE.xattr.", "SCHILY.xattr.")) for k in m.pax_headers)))' "$$f") || { \
			echo "verify-release: FAIL — $$f cannot be read for its pax headers (python3 tarfile)."; exit 1; }; \
		if [ -n "$$xh" ]; then \
			echo "verify-release: FAIL — $$f carries extended attributes as pax headers ($$xh)."; \
			echo "  macOS tar writes them unless called with --no-xattrs; COPYFILE_DISABLE alone does not."; \
			exit 1; fi; \
		own=$$(python3 -c 'import sys, tarfile; print(" ".join("%s(%d:%d %s:%s)" % (m.name, m.uid, m.gid, m.uname, m.gname) for m in tarfile.open(sys.argv[1]) if (m.uid, m.gid, m.uname, m.gname) != (0, 0, "root", "root")))' "$$f") || { \
			echo "verify-release: FAIL — $$f cannot be read for its owner fields (python3 tarfile)."; exit 1; }; \
		if [ -n "$$own" ]; then \
			echo "verify-release: FAIL — $$f records a builder identity in member owner fields: $$own"; \
			echo "  tar must be called with --uid 0 --gid 0 --uname root --gname root."; \
			exit 1; fi; \
		got=$$(printf '%s\n' "$$names" | LC_ALL=C sort | tr '\n' ' '); \
		want=$$(printf '%s\n' "$(BINARY)" README.md LICENSE | LC_ALL=C sort | tr '\n' ' '); \
		if [ "$$got" != "$$want" ]; then \
			echo "verify-release: FAIL — $$f holds $$got; expected $$want"; exit 1; fi; \
	done
	@tmp=$$(mktemp -d); rc=0; \
	for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = windows ] && ext=".exe"; \
		base="$(DIST_DIR)/$(BINARY)-$(VERSION)-$$os-$$arch"; \
		if [ "$$os" = linux ]; then tar -xzOf "$$base.tar.gz" "$(BINARY)" > "$$tmp/bin" 2>/dev/null; \
		else unzip -p "$$base.zip" "$(BINARY)$$ext" > "$$tmp/bin" 2>/dev/null; fi; \
		if [ ! -s "$$tmp/bin" ]; then \
			echo "verify-release: FAIL — cannot extract $(BINARY)$$ext from the $$os-$$arch archive."; rc=1; continue; fi; \
		if LC_ALL=C grep -a -q -E '/Users/|/home/' "$$tmp/bin"; then \
			echo "verify-release: FAIL — the $$os-$$arch binary embeds the builder's home-directory paths."; \
			echo "  make build-all passes -trimpath; a GOFLAGS override or a hand-run go build removed it."; rc=1; fi; \
	done; rm -rf "$$tmp"; exit $$rc
	@echo "verify-release: OK ($(VERSION), notarized, unpacks, runs, reports its version, clean linux archives, no builder paths)"

test:
	go test ./...

clean:
	rm -rf $(DIST_DIR)

# Homebrew tap generation (see scripts/release-brew.mk). After `make package`,
# `make brew` generates this formula from the built darwin-arm64 zip into the
# local nlink-jp/homebrew-tap checkout. The package target is unchanged.
BREW_KIND := formula
BREW_DESC := Authenticated webhook receiver that writes payloads to GCS
include scripts/release-brew.mk
