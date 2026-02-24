package vpn

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"nzb-connect/internal/config"
)

// WireGuardConnector manages a WireGuard tunnel by shelling out to ip and wg.
type WireGuardConnector struct {
	cfg *config.WireGuardConfig

	mu            sync.RWMutex
	status        ConnectorStatus
	ifName        string
	endpointRoute string // "host/32 via gw dev iface" stored for teardown
}

// NewWireGuardConnector creates a new WireGuard connector.
func NewWireGuardConnector(cfg *config.WireGuardConfig) *WireGuardConnector {
	return &WireGuardConnector{
		cfg:    cfg,
		status: ConnectorStatus{State: StateDisconnected},
	}
}

// Connect creates a WireGuard interface and configures it.
func (w *WireGuardConnector) Connect(ctx context.Context) error {
	w.mu.Lock()
	w.status = ConnectorStatus{State: StateConnecting}
	w.mu.Unlock()

	// Clean up any stale interfaces from previous runs
	w.cleanupStale()

	// Find available interface name
	ifName, err := w.findAvailableName()
	if err != nil {
		w.setError(err)
		return err
	}

	w.mu.Lock()
	w.ifName = ifName
	w.mu.Unlock()

	// Create the interface
	if err := w.run(ctx, "ip", "link", "add", ifName, "type", "wireguard"); err != nil {
		err = fmt.Errorf("create interface: %w", err)
		w.setError(err)
		return err
	}

	// Apply config by piping it to wg via stdin (/dev/stdin).
	// This avoids writing a temp file to /tmp, which AppArmor can restrict.
	if err := w.setconf(ctx, ifName); err != nil {
		w.teardown(ifName)
		w.setError(fmt.Errorf("setconf: %w", err))
		return err
	}

	// Add address
	if w.cfg.Address != "" {
		if err := w.run(ctx, "ip", "addr", "add", w.cfg.Address, "dev", ifName); err != nil {
			w.teardown(ifName)
			w.setError(fmt.Errorf("add address: %w", err))
			return err
		}
	}

	// Bring interface up
	if err := w.run(ctx, "ip", "link", "set", ifName, "up"); err != nil {
		w.teardown(ifName)
		w.setError(fmt.Errorf("link up: %w", err))
		return err
	}

	// Set up routing (like wg-quick) so packets bound to this interface have a route.
	// If routing setup fails, teardown restores system routing before returning.
	if err := w.setupRouting(ctx, ifName); err != nil {
		w.teardown(ifName)
		w.setError(fmt.Errorf("routing setup: %w", err))
		return err
	}

	// Configure DNS via resolvconf or resolvectl if available
	if w.cfg.DNS != "" {
		w.setupDNS(ctx, ifName)
	}

	// Verify the tunnel is actually live by waiting for a WireGuard handshake.
	// This catches wrong keys, blocked ports, and unreachable endpoints early
	// rather than silently routing all traffic to a dead tunnel.
	if err := w.waitForHandshake(ctx, ifName); err != nil {
		w.teardown(ifName)
		w.setError(err)
		return err
	}

	w.mu.Lock()
	w.status = ConnectorStatus{
		State:         StateConnected,
		InterfaceName: ifName,
		ConnectedAt:   time.Now(),
	}
	w.mu.Unlock()

	log.Printf("WireGuard interface %s is up", ifName)
	return nil
}

// Disconnect tears down the WireGuard interface.
func (w *WireGuardConnector) Disconnect() error {
	w.mu.RLock()
	ifName := w.ifName
	w.mu.RUnlock()

	if ifName == "" {
		return nil
	}

	err := w.teardown(ifName)

	w.mu.Lock()
	w.ifName = ""
	w.status = ConnectorStatus{State: StateDisconnected}
	w.mu.Unlock()

	return err
}

// Status returns the current connector status.
func (w *WireGuardConnector) Status() ConnectorStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.status
}

// InterfaceName returns the created interface name.
func (w *WireGuardConnector) InterfaceName() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.ifName
}

func (w *WireGuardConnector) findAvailableName() (string, error) {
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("wg%d", i)
		if _, err := net.InterfaceByName(name); err != nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no available WireGuard interface name (wg0-wg9 all in use)")
}

// buildConfig returns the wg setconf-compatible config as a string.
// DNS is intentionally omitted — it is not a wg directive; we set it via resolvconf/systemd-resolved separately.
func (w *WireGuardConnector) buildConfig() string {
	conf := "[Interface]\n"
	conf += fmt.Sprintf("PrivateKey = %s\n", w.cfg.PrivateKey)
	if w.cfg.ListenPort > 0 {
		conf += fmt.Sprintf("ListenPort = %d\n", w.cfg.ListenPort)
	}

	conf += "\n[Peer]\n"
	conf += fmt.Sprintf("PublicKey = %s\n", w.cfg.PeerPublicKey)
	if w.cfg.PresharedKey != "" {
		conf += fmt.Sprintf("PresharedKey = %s\n", w.cfg.PresharedKey)
	}
	if w.cfg.PeerEndpoint != "" {
		conf += fmt.Sprintf("Endpoint = %s\n", w.cfg.PeerEndpoint)
	}

	allowedIPs := w.cfg.AllowedIPs
	if allowedIPs == "" {
		allowedIPs = "0.0.0.0/0"
	}
	conf += fmt.Sprintf("AllowedIPs = %s\n", allowedIPs)

	if w.cfg.PersistentKeepalive > 0 {
		conf += fmt.Sprintf("PersistentKeepalive = %d\n", w.cfg.PersistentKeepalive)
	}
	return conf
}

// setconf pipes the WireGuard config to "wg setconf <iface> /dev/stdin".
// Piping avoids writing a temp file to /tmp which AppArmor may block.
func (w *WireGuardConnector) setconf(ctx context.Context, ifName string) error {
	cmd := exec.CommandContext(ctx, resolveCmd("wg"), "setconf", ifName, "/dev/stdin")
	cmd.Stdin = strings.NewReader(w.buildConfig())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg setconf: %w (%s)", err, string(out))
	}
	return nil
}

const wgFWMark = 51820
const wgRouteTable = 51820

func (w *WireGuardConnector) setupRouting(ctx context.Context, ifName string) error {
	// Add endpoint protection route BEFORE redirecting non-fwmark traffic to
	// table 51820.  Without this, the peer endpoint itself would be routed
	// through the (still-empty) tunnel table and become unreachable during
	// setup — the standard wg-quick pattern for full-tunnel configs.
	endpointRoute, err := w.addEndpointRoute(ctx)
	if err != nil {
		// Non-fatal: log and continue.  Missing the route is only a problem if
		// the main routing table loses its default gateway later.
		log.Printf("Warning: endpoint protection route skipped: %v", err)
	}
	w.mu.Lock()
	w.endpointRoute = endpointRoute
	w.mu.Unlock()

	// Mark WireGuard's own encrypted packets so they bypass our routing rules
	// (prevents the encrypted UDP packets from looping back through the tunnel)
	if err := w.run(ctx, "wg", "set", ifName, "fwmark", fmt.Sprintf("%d", wgFWMark)); err != nil {
		return fmt.Errorf("set fwmark: %w", err)
	}

	// Add routes to a separate routing table for the AllowedIPs
	allowedIPs := w.cfg.AllowedIPs
	if allowedIPs == "" {
		allowedIPs = "0.0.0.0/0"
	}

	for _, cidr := range strings.Split(allowedIPs, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if cidr == "0.0.0.0/0" {
			// Use two /1 routes to override default without replacing it
			if err := w.run(ctx, "ip", "route", "add", "0.0.0.0/1", "dev", ifName,
				"table", fmt.Sprintf("%d", wgRouteTable)); err != nil {
				return fmt.Errorf("add route 0.0.0.0/1 to table %d: %w", wgRouteTable, err)
			}
			if err := w.run(ctx, "ip", "route", "add", "128.0.0.0/1", "dev", ifName,
				"table", fmt.Sprintf("%d", wgRouteTable)); err != nil {
				return fmt.Errorf("add route 128.0.0.0/1 to table %d: %w", wgRouteTable, err)
			}
		} else {
			if err := w.run(ctx, "ip", "route", "add", cidr, "dev", ifName,
				"table", fmt.Sprintf("%d", wgRouteTable)); err != nil {
				return fmt.Errorf("add route %s to table %d: %w", cidr, wgRouteTable, err)
			}
		}
	}

	// Route rule: all traffic WITHOUT our fwmark uses the WireGuard routing table.
	// WireGuard's own encrypted packets carry the fwmark and skip this rule.
	if err := w.run(ctx, "ip", "rule", "add", "not", "fwmark", fmt.Sprintf("%d", wgFWMark),
		"table", fmt.Sprintf("%d", wgRouteTable), "priority", "1000"); err != nil {
		return fmt.Errorf("add ip rule (fwmark→table %d): %w", wgRouteTable, err)
	}

	// Ensure local/LAN routes in main table still work (suppress default-only matches)
	if err := w.run(ctx, "ip", "rule", "add", "table", "main",
		"suppress_prefixlength", "0", "priority", "999"); err != nil {
		// "File exists" means the rule is already in place — safe to continue
		if !strings.Contains(err.Error(), "File exists") {
			return fmt.Errorf("add ip rule (main suppress): %w", err)
		}
	}

	log.Printf("WireGuard routing configured (table %d, fwmark %d)", wgRouteTable, wgFWMark)
	return nil
}

// addEndpointRoute adds a host route for the WireGuard peer endpoint via the
// current default gateway so that WireGuard's encrypted UDP still reaches the
// peer after we redirect all non-fwmark traffic to table 51820.
func (w *WireGuardConnector) addEndpointRoute(ctx context.Context) (string, error) {
	endpoint := w.cfg.PeerEndpoint
	if endpoint == "" {
		return "", nil
	}

	// Strip port (format: "host:port" or "[ipv6]:port")
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint // already bare IP
	}

	// Find current default gateway from the main routing table
	cmd := exec.CommandContext(ctx, resolveCmd("ip"), "route", "show", "0.0.0.0/0")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("query default route: %w", err)
	}

	var gateway, dev string
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Fields(line)
		for i, p := range parts {
			if p == "via" && i+1 < len(parts) {
				gateway = parts[i+1]
			}
			if p == "dev" && i+1 < len(parts) {
				dev = parts[i+1]
			}
		}
		if gateway != "" && dev != "" {
			break
		}
	}

	if gateway == "" || dev == "" {
		return "", fmt.Errorf("could not determine default gateway from main routing table")
	}

	if err := w.run(ctx, "ip", "route", "add", host+"/32", "via", gateway, "dev", dev); err != nil {
		if strings.Contains(err.Error(), "File exists") {
			// Route already exists — record it for cleanup but don't fail
			log.Printf("Endpoint protection route already present: %s/32 via %s dev %s", host, gateway, dev)
			return fmt.Sprintf("%s/32 via %s dev %s", host, gateway, dev), nil
		}
		return "", fmt.Errorf("add endpoint protection route: %w", err)
	}

	route := fmt.Sprintf("%s/32 via %s dev %s", host, gateway, dev)
	log.Printf("Added endpoint protection route: %s", route)
	return route, nil
}

func (w *WireGuardConnector) teardownRouting(ctx context.Context) {
	t := fmt.Sprintf("%d", wgRouteTable)
	fm := fmt.Sprintf("%d", wgFWMark)
	// Best-effort cleanup of rules and routes
	w.run(ctx, "ip", "rule", "del", "not", "fwmark", fm, "table", t, "priority", "1000")
	w.run(ctx, "ip", "rule", "del", "table", "main", "suppress_prefixlength", "0", "priority", "999")
	w.run(ctx, "ip", "route", "flush", "table", t)

	// Remove endpoint protection route added during setup
	w.mu.RLock()
	endpointRoute := w.endpointRoute
	w.mu.RUnlock()

	if endpointRoute != "" {
		// endpointRoute format: "host/32 via gateway dev iface"
		parts := strings.Fields(endpointRoute)
		if len(parts) == 5 {
			w.run(ctx, "ip", "route", "del", parts[0], parts[1], parts[2], parts[3], parts[4])
		}
		w.mu.Lock()
		w.endpointRoute = ""
		w.mu.Unlock()
	}
}

func (w *WireGuardConnector) teardown(ifName string) error {
	ctx := context.Background()
	// Best-effort: tear down routing, DNS, bring interface down, delete
	w.teardownRouting(ctx)
	w.teardownDNS(ctx, ifName)
	w.run(ctx, "ip", "link", "set", ifName, "down")
	return w.run(ctx, "ip", "link", "delete", ifName)
}

// waitForHandshake polls wg show latest-handshakes until the peer completes a
// cryptographic handshake, confirming the tunnel is live.  Times out after 30s.
func (w *WireGuardConnector) waitForHandshake(ctx context.Context, ifName string) error {
	log.Printf("Waiting for WireGuard handshake on %s (timeout 30s)...", ifName)

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("WireGuard handshake timed out after 30s — peer unreachable or keys mismatch")
		case <-ticker.C:
			ts, err := w.latestHandshake(ctx, ifName)
			if err != nil {
				log.Printf("WireGuard handshake check error: %v", err)
				continue
			}
			// A handshake within the last 5 seconds means the tunnel is live.
			// WireGuard handshakes expire after 180s; a fresh one confirms connectivity.
			if !ts.IsZero() && time.Since(ts) < 5*time.Second {
				log.Printf("WireGuard handshake confirmed at %s", ts.Format(time.RFC3339))
				return nil
			}
		}
	}
}

// latestHandshake runs "wg show <ifName> latest-handshakes" and returns the
// most recent handshake timestamp across all configured peers.
func (w *WireGuardConnector) latestHandshake(ctx context.Context, ifName string) (time.Time, error) {
	cmd := exec.CommandContext(ctx, resolveCmd("wg"), "show", ifName, "latest-handshakes")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("wg show latest-handshakes: %w", err)
	}

	// Output format: "<pubkey>\t<unix-timestamp>\n"  (one line per peer)
	var latest time.Time
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		tsUnix, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || tsUnix == 0 {
			continue
		}
		t := time.Unix(tsUnix, 0)
		if t.After(latest) {
			latest = t
		}
	}
	return latest, nil
}

func (w *WireGuardConnector) setupDNS(ctx context.Context, ifName string) {
	dnsServers := strings.Split(strings.TrimSpace(w.cfg.DNS), ",")
	for i, s := range dnsServers {
		dnsServers[i] = strings.TrimSpace(s)
	}

	// Build nameserver lines for resolvconf
	var resolvconfInput strings.Builder
	for _, s := range dnsServers {
		if s != "" {
			resolvconfInput.WriteString("nameserver " + s + "\n")
		}
	}

	// Try resolvconf first (Debian/Ubuntu style)
	cmd := exec.CommandContext(ctx, resolveCmd("resolvconf"), "-a", ifName, "-m", "0", "-x")
	cmd.Stdin = strings.NewReader(resolvconfInput.String())
	if err := cmd.Run(); err == nil {
		log.Printf("DNS configured via resolvconf for %s: %s", ifName, w.cfg.DNS)
		return
	} else {
		log.Printf("resolvconf DNS setup failed: %v; trying resolvectl", err)
	}

	// Fallback: resolvectl (Fedora/Arch/systemd-resolved systems)
	var validServers []string
	for _, s := range dnsServers {
		if s != "" {
			validServers = append(validServers, s)
		}
	}
	if len(validServers) == 0 {
		return
	}

	resolvectlArgs := append([]string{"dns", ifName}, validServers...)
	if err := exec.CommandContext(ctx, resolveCmd("resolvectl"), resolvectlArgs...).Run(); err != nil {
		log.Printf("resolvectl dns setup failed (non-fatal): %v", err)
		return
	}
	// Make this interface the default DNS scope for all queries ("~." = catch-all domain)
	if err := exec.CommandContext(ctx, resolveCmd("resolvectl"), "domain", ifName, "~.").Run(); err != nil {
		log.Printf("resolvectl domain setup failed (non-fatal): %v", err)
		return
	}
	log.Printf("DNS configured via resolvectl for %s: %s", ifName, w.cfg.DNS)
}

func (w *WireGuardConnector) teardownDNS(ctx context.Context, ifName string) {
	// Try resolvconf first; fall back to resolvectl revert
	cmd := exec.CommandContext(ctx, resolveCmd("resolvconf"), "-d", ifName)
	if err := cmd.Run(); err != nil {
		exec.CommandContext(ctx, resolveCmd("resolvectl"), "revert", ifName).Run() // best-effort
	}
}

func (w *WireGuardConnector) cleanupStale() {
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("wg%d", i)
		iface, err := net.InterfaceByName(name)
		if err != nil {
			continue
		}
		// If the interface exists but is down, it might be stale from a previous run
		if iface.Flags&net.FlagUp == 0 {
			log.Printf("Cleaning up stale WireGuard interface %s", name)
			w.teardown(name)
		}
	}
}

func (w *WireGuardConnector) run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, resolveCmd(name), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)", name, args, err, string(out))
	}
	return nil
}

func (w *WireGuardConnector) setError(err error) {
	w.mu.Lock()
	w.status = ConnectorStatus{
		State: StateError,
		Error: err.Error(),
	}
	w.mu.Unlock()
	log.Printf("WireGuard error: %v", err)
}
