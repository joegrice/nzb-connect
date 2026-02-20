# Prompt: Build NZBGet-VPN MVP in Go

I need you to build a simplified Usenet downloader in Go with VPN binding and ARR stack compatibility. This is an MVP focusing on core functionality only.

## Required Features

### 1. VPN Interface Binding
- Force ALL network traffic through a specified network interface (e.g., tun0, wg0)
- Use Go's net.Dialer with Control function and SO_BINDTODEVICE socket option
- Monitor interface availability - if VPN drops, pause downloads
- No VPN connection management needed (user configures VPN externally)

### 2. ARR Stack Integration (Sonarr/Radarr/Lidarr Compatibility)
Implement SABnzbd-compatible API endpoints:

**POST /api** - Add NZB
- Accept multipart/form-data with "nzbfile" field (file upload)
- Accept URL in "name" field (download NZB from URL)
- Optional "cat" field for category
- Return: `{"status": true, "nzo_ids": ["<id>"]}`

**GET /api?mode=history**
- Return completed downloads in SABnzbd format
- Include: name, category, status, storage path, download_time

**GET /api?mode=queue**
- Return current downloads with progress
- Include: name, size, downloaded MB, percentage, speed

**GET /api?mode=status**
- Return: paused state, speed (KB/s), remaining MB

### 3. NNTP Server Management

**Web UI** - Simple HTML form to add/edit/delete servers
**API Endpoints**:
- GET /api/servers - List all servers
- POST /api/servers - Add new server (with connection test)
- DELETE /api/servers/:id - Remove server
- PUT /api/servers/:id - Update server

**Server Properties**:
- Name, host, port, SSL (true/false)
- Username, password
- Number of connections (1-50)
- Enabled/disabled flag

**Connection Testing**: Before saving, test NNTP connection and verify 200/201 response

### 4. Download & Unpack Pipeline

**Download Flow**:
1. Parse NZB file (XML) to extract files and segments
2. Download segments via NNTP (using VPN-bound connections)
3. Decode YEnc format
4. Assemble segments into complete files
5. Save to incomplete directory

**Post-Processing**:
1. Extract archives (unrar for .rar, 7z for .zip/.7z)
2. Move extracted files to complete directory
3. Delete archives and temp files
4. Mark as complete in history

**Directories**:
- /downloads/incomplete - Active downloads
- /downloads/complete - Finished downloads
- /tmp - Temporary segment cache

### 5. Basic Web UI
Simple HTML interface (no framework needed) with:
- Dashboard showing current downloads with progress bars
- Form to add NNTP servers
- VPN status indicator (interface up/down)
- Download history table
- Basic CSS for readability

---

## Technical Requirements

### Language & Structure
- Go 1.22+
- Use standard library where possible
- Minimal external dependencies

### Recommended Libraries
- **Web**: `github.com/gin-gonic/gin` (or stdlib net/http)
- **Database**: `github.com/mattn/go-sqlite3` (for queue/history persistence)
- **Config**: `gopkg.in/yaml.v3`
- **Logging**: `github.com/sirupsen/logrus`
- **NZB parsing**: Use encoding/xml (stdlib)
- **YEnc**: Write custom decoder (not complex)

### Project Structure
```
nzbget-vpn/
├── cmd/
│   └── nzbget-vpn/
│       └── main.go
├── internal/
│   ├── api/          # HTTP handlers, ARR API
│   ├── config/       # Configuration management
│   ├── downloader/   # NNTP client, download engine
│   ├── nzb/          # NZB parser
│   ├── postprocess/  # Extraction and file moving
│   ├── vpn/          # VPN interface binding
│   └── queue/        # Download queue management
├── web/
│   └── static/       # HTML, CSS, JS
├── config.yaml
├── go.mod
├── go.sum
└── README.md
```

---

## Detailed Implementation Instructions

### VPN Binding (internal/vpn/bind.go)

Create a net.Dialer that binds to specified interface:

```go
// Key function signature
func BindToInterface(interfaceName string) (*net.Dialer, error)

// Use syscall.SO_BINDTODEVICE on Linux
// Monitor interface with net.InterfaceByName and check Flags&net.FlagUp
```

### NNTP Client (internal/downloader/nntp.go)

Implement basic NNTP commands:
- Connect (with SSL support via tls.Dial)
- Authenticate: `AUTHINFO USER <username>` then `AUTHINFO PASS <password>`
- Select group: `GROUP <newsgroup>`
- Fetch article: `ARTICLE <message-id>` or `BODY <message-id>`
- Parse multiline response (ends with ".")

Connection pool: Maintain pool of connections per server for concurrency

### YEnc Decoder (internal/downloader/yenc.go)

YEnc format structure:
```
=ybegin line=128 size=123456 name=file.bin
<encoded data>
=yend size=123456 crc32=abcd1234
```

Decoding: Each byte is offset by 42, handle escape sequences (=y -> subtract 64)

### NZB Parser (internal/nzb/parser.go)

Parse XML structure:
```xml
<nzb>
  <file subject="..." poster="...">
    <groups><group>alt.binaries.test</group></groups>
    <segments>
      <segment bytes="384000" number="1">msg-id@news.server</segment>
    </segments>
  </file>
</nzb>
```

Use encoding/xml with struct tags

### Queue Manager (internal/queue/manager.go)

Store queue state in SQLite:
- Table: downloads (id, name, status, progress, path, created_at)
- Table: history (id, name, status, completed_at, path)

Status values: queued, downloading, processing, completed, failed

### Post-Processing (internal/postprocess/extract.go)

Shell out to external tools:
- Unrar: `unrar x -o+ archive.rar dest/`
- 7zip: `7z x archive.zip -odest/`

Parse output for progress/errors, move files after extraction

### API Handler (internal/api/sabnzbd.go)

Gin routes matching SABnzbd API format
Parse query parameters for mode, output (json/xml)
Return proper JSON structure for ARR compatibility

### Configuration (config.yaml)

```yaml
vpn:
  interface: tun0  # Network interface to bind to
  
servers:
  - name: primary
    host: news.example.com
    port: 563
    ssl: true
    username: user
    password: pass
    connections: 20

paths:
  incomplete: /downloads/incomplete
  complete: /downloads/complete
  temp: /tmp/nzbget

web:
  port: 6789
  username: admin
  password: changeme

postprocess:
  unrar: /usr/bin/unrar
  sevenzip: /usr/bin/7z
  delete_archives: true
```

---

## Non-Requirements (Explicitly Skip)

❌ Par2 verification/repair
❌ RSS feeds or indexer integration
❌ Advanced queue controls (pause/resume individual items)
❌ Categories and complex routing
❌ Scheduling
❌ Download speed limiting
❌ Multiple VPN provider support (just interface binding)
❌ Elaborate web UI (keep it minimal)

---

## Success Criteria

The MVP is complete when:

1. ✅ Sonarr/Radarr can add downloads via API
2. ✅ All NNTP traffic goes through VPN interface
3. ✅ NZB files download, decode, and assemble correctly
4. ✅ Archives extract automatically
5. ✅ Files move to complete directory
6. ✅ Download history visible in web UI
7. ✅ Can add/remove NNTP servers via UI
8. ✅ If VPN drops, downloads pause

---

## Example Usage Flow

1. User starts app: `./nzbget-vpn --config config.yaml`
2. App binds to tun0 interface (VPN must be running)
3. User adds NNTP server via web UI at http://localhost:6789
4. Sonarr sends NZB to POST http://localhost:6789/api
5. App downloads articles via NNTP, decodes YEnc
6. Assembles files in /downloads/incomplete
7. Extracts .rar files
8. Moves to /downloads/complete
9. Notifies Sonarr via history API

---

## Implementation Priority

Build in this order:

**Phase 1: Foundation (Day 1-2)**
1. Configuration loading (config.yaml)
2. VPN interface binding
3. Basic NNTP client (connect, auth, fetch)
4. YEnc decoder

**Phase 2: Core Download (Day 3-4)**
1. NZB parser
2. Download engine with worker pool
3. File assembly
4. Basic queue management

**Phase 3: Post-Processing (Day 5)**
1. Archive extraction (unrar/7z)
2. File moving
3. Cleanup

**Phase 4: API & UI (Day 6-7)**
1. SABnzbd-compatible API
2. Server management endpoints
3. Simple web UI
4. History tracking

---

## Testing Checklist

- [ ] VPN binding: Verify all connections use correct interface
- [ ] NNTP: Successfully connect to real news server
- [ ] YEnc: Decode test article correctly
- [ ] NZB: Parse real NZB file
- [ ] Download: Complete small NZB (few MB)
- [ ] Extract: Unpack .rar archive
- [ ] ARR API: Sonarr can add and query downloads
- [ ] Server Management: Add/remove servers via UI
- [ ] VPN Failover: Pause when interface goes down

---

## Error Handling Requirements

- Log all errors with context (which segment, which server)
- Retry failed segments 3 times with exponential backoff
- If all servers fail, mark download as failed
- If VPN drops, pause all downloads immediately
- If extraction fails, keep files in incomplete folder

---

## Code Style Preferences

- Use descriptive variable names
- Add comments for complex logic
- Keep functions under 50 lines where possible
- Use context.Context for cancellation
- Proper error wrapping with fmt.Errorf
- Unit tests for YEnc decoder and NZB parser

---

Please implement this complete working application with all source files. Include:

1. All Go source code files with full implementation
2. config.yaml with example configuration
3. go.mod with dependencies
4. README.md with build and usage instructions
5. Basic HTML/CSS for web UI
6. Example NZB file for testing

---

## Implementation Status (as of 2026-02-20)

### What Was Built

All phases from the implementation plan are complete. Key files:

| Package | File | Purpose |
|---|---|---|
| `internal/vpn` | `bind.go` | SO_BINDTODEVICE binding (bind-only mode) |
| `internal/vpn` | `wireguard.go` | Managed WireGuard connector |
| `internal/vpn` | `openvpn.go` | Managed OpenVPN connector |
| `internal/vpn` | `connector.go` | VPN manager / interface monitor |
| `internal/downloader` | `engine.go` | Download engine, worker pool |
| `internal/downloader` | `nntp.go` | NNTP client with connection pool |
| `internal/downloader` | `yenc.go` | YEnc decoder |
| `internal/nzb` | `parser.go` | NZB XML parser incl. `<meta>` |
| `internal/postprocess` | `extract.go` | Archive extraction pipeline |
| `internal/queue` | `manager.go` | SQLite-backed queue/history |
| `internal/api` | `sabnzbd.go` | SABnzbd-compatible API |
| `internal/config` | `config.go` | YAML config + chown helper |

### Deviations & Additions Beyond Original Spec

**RAR extraction strategy** (`internal/postprocess/extract.go`):
- Uses `github.com/nwaples/rardecode/v2` (not v1) — v2 supports RAR5 natively in pure Go
- Extraction order: pure-Go rardecode/v2 → external `unrar` → external `7z`
- External tools are only needed for genuinely encrypted archives

**NZB password support** (`internal/nzb/parser.go`, `internal/postprocess/extract.go`):
- Parser now reads `<head><meta type="password">...</meta></head>` from NZB files
- Password is automatically passed to rardecode/v2 (`Password()` option) and unrar (`-p<pass>`)
- This is how NZBGet handles password-protected posts; without it, extraction fails with exit 11

**VPN modes** (not in original spec):
- Two modes: **bind-only** (original spec — user manages VPN, app binds to interface) and **managed** (app brings up WireGuard or OpenVPN using system tools)
- Managed mode requires root and shells out to `wg`, `ip`, `openvpn`

**Extraction on failure**:
- If extraction fails, raw archives are moved to the complete directory (not left in incomplete) so Sonarr/Radarr and the user can still find them

### Known Limitations

- No Par2 repair (by design — see Non-Requirements above)
- Managed WireGuard mode requires `unrar` from RPMFusion on Fedora (for truly encrypted archives)
- 7z not installed by default on Fedora; install `p7zip-plugins` if needed

Focus on working, production-ready code that handles errors gracefully. Make it simple but robust.
