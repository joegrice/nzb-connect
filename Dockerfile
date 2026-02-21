# syntax=docker/dockerfile:1

# ── Frontend build stage ───────────────────────────────────────────────────────
FROM node:20-slim AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ── Go build stage ────────────────────────────────────────────────────────────
FROM golang:1.22-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Copy the pre-built frontend assets so they get embedded into the binary
COPY --from=frontend /web/dist ./web/dist
# CGO is required by go-sqlite3 (embeds the sqlite3 C library at compile time,
# so the runtime image needs no libsqlite3 package).
RUN CGO_ENABLED=1 GOOS=linux go build -o nzb-connect ./cmd/nzb-connect/


# ── Runtime stage ──────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# Archive extraction:
#   unrar     – best RAR support (RAR2–5, encrypted); from non-free repo
#   7zip      – fallback extractor; also handles zip/7z/rar
# The app also has a built-in pure-Go RAR decoder (no binary needed for RAR2–4).
#
# VPN tooling (only needed when protocol=wireguard or protocol=openvpn):
#   wireguard-tools  – wg(8) for managed WireGuard mode
#   iproute2         – ip(8) for routing rules
#   openvpn          – for managed OpenVPN mode
#
# TLS certificates for outbound HTTPS/NNTP-SSL.
RUN echo "deb http://deb.debian.org/debian bookworm non-free non-free-firmware" \
        >> /etc/apt/sources.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
        unrar \
        7zip \
        ca-certificates \
        wireguard-tools \
        iproute2 \
        openvpn \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/nzb-connect .
# web/dist is embedded in the binary — no separate COPY needed

# Default config — copied to /config/config.yaml on first run if none exists
COPY config.example.yaml .
COPY docker-entrypoint.sh .
RUN chmod +x docker-entrypoint.sh

# /config     – base data directory: config.yaml + cache/  (mount a host directory here)
# /downloads  – download root: incomplete/ and complete/ created automatically
VOLUME ["/config", "/downloads"]

EXPOSE 6789

ENTRYPOINT ["/app/docker-entrypoint.sh"]
