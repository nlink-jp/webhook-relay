# The builder is pinned to one Go release by tag and by the digest of its
# multi-arch index: the tag says which release, the digest is what is pulled.
# The official image sets GOTOOLCHAIN=local, so it must be at least the `go`
# line of go.mod or the build stops at `go mod download` — golang:1.24 did,
# from the day go.mod moved to 1.26. Move the tag, the digest and go.mod
# together, to a release whose standard library has the fixes published so far.
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo docker)" -o webhook-relay .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && adduser -D -H appuser
USER appuser
COPY --from=builder /build/webhook-relay /usr/local/bin/webhook-relay

ENTRYPOINT ["webhook-relay"]
