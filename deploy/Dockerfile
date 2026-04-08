FROM golang:1.24-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo docker)" -o webhook-relay .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && adduser -D -H appuser
USER appuser
COPY --from=builder /build/webhook-relay /usr/local/bin/webhook-relay

ENTRYPOINT ["webhook-relay"]
