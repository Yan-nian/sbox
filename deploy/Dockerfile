# ==============================================================================
# Stage 1: Build React + TypeScript + Vite frontend into web/dist
# ==============================================================================
FROM node:22-alpine AS web-builder

WORKDIR /src/web

# Install dependencies in a cached layer and move node_modules aside so copying
# web/ cannot overwrite Linux binaries if a host web/node_modules exists.
COPY web/package.json web/package-lock.json* ./
RUN if [ -f package-lock.json ]; then \
      npm ci --no-audit --no-fund; \
    else \
      npm install --no-audit --no-fund; \
    fi \
    && mv node_modules /tmp/node_modules

COPY web/ ./
RUN rm -rf node_modules dist \
    && mv /tmp/node_modules ./node_modules \
    && npm run build

# ==============================================================================
# Stage 2: Build static Go binary with embedded web/dist
# ==============================================================================
FROM golang:1.23-alpine AS go-builder

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=0.1.0

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/embed.go ./web/embed.go
COPY --from=web-builder /src/web/dist ./web/dist

RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /out/sbox-panel ./cmd/sbox-panel

# ==============================================================================
# Stage 3: Minimal Alpine runtime image
# ==============================================================================
FROM alpine:3.21

LABEL org.opencontainers.image.title="sbox-panel" \
      org.opencontainers.image.description="Lightweight self-hosted sing-box management panel" \
      org.opencontainers.image.source="https://github.com/sbox-panel/sbox-panel"

RUN apk add --no-cache ca-certificates tzdata curl tar gzip \
    && mkdir -p /data

COPY --from=go-builder /out/sbox-panel /usr/local/bin/sbox-panel
RUN chmod 0755 /usr/local/bin/sbox-panel

ENV SBOX_DATA_DIR=/data \
    SBOX_LISTEN=0.0.0.0:18080 \
    TZ=UTC

VOLUME ["/data"]
WORKDIR /data
EXPOSE 18080

ENTRYPOINT ["/usr/local/bin/sbox-panel"]
CMD ["serve", "--data-dir", "/data", "--listen", "0.0.0.0:18080"]
