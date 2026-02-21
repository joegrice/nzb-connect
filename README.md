# NZB Connect

A lightweight Usenet downloader with VPN binding, written in Go.
Compatible with Sonarr, Radarr, and other \*arr apps via the SABnzbd API.

## Features

- **SABnzbd-compatible API** — drop-in replacement for Sonarr/Radarr/Lidarr
- **VPN binding** — all NNTP traffic is forced through a specified network interface; downloads pause automatically if the VPN drops
- **Managed VPN** — optionally let the app bring up WireGuard or OpenVPN for you (requires root)
- **Automatic extraction** — unpacks RAR (including RAR5), ZIP, and 7z archives; supports password-protected archives via NZB `<meta type="password">` tags
- **Web UI** — dark-themed React interface with live download progress, history, server management, and VPN controls

## Requirements

- Go 1.22+
- Node 18+ (to build the UI)
- Linux (uses `SO_BINDTODEVICE` for interface binding)
- For managed WireGuard: `ip`, `wg` in `$PATH`, and root/sudo
- For managed OpenVPN: `openvpn` in `$PATH`, and root/sudo
- `unrar` recommended for fastest RAR extraction (falls back to pure-Go rardecode, then `7z`)

## Quick Start

```bash
# 1. Clone and build
git clone https://github.com/yourname/nzb-connect
cd nzb-connect

# Build the UI
cd web && npm install && npm run build && cd ..

# Build the binary
go build -o nzb-connect ./cmd/nzb-connect/

# 2. Configure
cp config.yaml.example config.yaml
$EDITOR config.yaml   # add your NNTP server and VPN credentials

# 3. Run
# Managed VPN mode requires root:
sudo ./nzb-connect --config config.yaml

# Bind-only mode (you manage the VPN yourself):
./nzb-connect --config config.yaml
```

Open `http://localhost:6789` in your browser.

## Configuration

Copy `config.yaml.example` to `config.yaml`. The file is git-ignored so your credentials stay local.

### VPN modes

| Mode | How it works |
|------|-------------|
| **Managed WireGuard** | Set `protocol: wireguard` and fill in `wireguard:` block. The app creates the interface and tears it down on exit. Requires root. |
| **Managed OpenVPN** | Set `protocol: openvpn` and fill in `openvpn:` block. Requires root. |
| **Bind-only** | Leave `protocol:` empty, set `interface: tun0` (or whatever your VPN creates). You manage the VPN; the app just binds sockets to that interface. |

The Connect/Disconnect button in the UI persists across restarts — if you disconnect, the app stays disconnected on the next start until you connect again.

### ARR integration

Point Sonarr/Radarr at the SABnzbd-compatible API:

```
Host:     localhost
Port:     6789
API Key:  (any value — not currently validated)
URL Path: /api
```

Set the download client type to **SABnzbd**.

## Development

```bash
# Terminal 1 — Go backend (auto-picks up UI from dist/)
go run ./cmd/nzb-connect/

# Terminal 2 — Vite dev server with HMR (proxies /api to :6789)
cd web && npm run dev
```

Then open `http://localhost:5173`.

## Archive extraction

Extraction is attempted in this order:

1. **unrar** (external) — fastest, full RAR5 + encryption support
2. **rardecode/v2** (pure Go) — RAR5 support, no binary needed; used as fallback
3. **7z** (external) — last resort for ZIP, 7z, and exotic formats

If extraction fails, the raw archives are moved to the complete directory so you can handle them manually or let the \*arr app retry.

Password-protected archives are supported when the NZB file contains a `<meta type="password">` tag.

## Building for production

```bash
cd web && npm run build && cd ..
go build -o nzb-connect ./cmd/nzb-connect/
```

The binary embeds the compiled UI — a single file to deploy.
