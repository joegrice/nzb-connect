# syntax=docker/dockerfile:1

# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.22-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
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
COPY web/ web/

# /config  – mount your config.yaml here
# /downloads – incomplete/ and complete/ subdirectories created automatically
VOLUME ["/config", "/downloads"]

EXPOSE 6789

# WireGuard / OpenVPN in managed mode require NET_ADMIN (and optionally
# SYS_MODULE to load kernel modules).  Run with:
#
#   docker run -d \
#     --cap-add NET_ADMIN \
#     --cap-add SYS_MODULE \
#     --sysctl net.ipv4.conf.all.src_valid_mark=1 \
#     -v $(pwd)/config.yaml:/config/config.yaml \
#     -v /downloads:/downloads \
#     -p 6789:6789 \
#     nzb-connect
#
# If you manage the VPN externally (protocol: "" in config), --cap-add NET_ADMIN
# is still needed for SO_BINDTODEVICE (binding NNTP connections to an interface).

ENTRYPOINT ["/app/nzb-connect"]
CMD ["--config", "/config/config.yaml"]
