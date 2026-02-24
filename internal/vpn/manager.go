package vpn

import (
	"context"
	"log"
	"sync"
	"time"

	"nzb-connect/internal/config"
)

// Manager orchestrates VPN connectivity. In managed mode it uses a Connector
// to create the tunnel, then starts a Monitor on the resulting interface.
// In passive mode (legacy) it only runs the Monitor on a pre-existing interface.
type Manager struct {
	cfg *config.Config

	mu        sync.RWMutex
	connector Connector
	monitor   *Monitor
	managed   bool // true when we own the VPN connection

	onDown func()
	onUp   func(interfaceName string)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Reconnection state
	reconnecting  bool
	reconnectMu   sync.Mutex
	reconfigureMu sync.Mutex
}

// NewManager creates a Manager from the current config. If config specifies
// a protocol (wireguard/openvpn) it enters managed mode; otherwise passive.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg: cfg,
	}
}

// OnDown registers a callback for when the VPN goes down.
func (m *Manager) OnDown(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDown = fn
}

// OnUp registers a callback for when the VPN comes up. The callback receives
// the interface name so callers can rebind connections.
func (m *Manager) OnUp(fn func(interfaceName string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUp = fn
}

// Start initializes the manager based on current config.
func (m *Manager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)

	vpnCfg := m.cfg.GetVPN()

	switch vpnCfg.Protocol {
	case "wireguard":
		if vpnCfg.WireGuard == nil {
			log.Println("VPN protocol set to wireguard but no wireguard config found, falling back to passive mode")
			m.startPassive(vpnCfg.Interface)
			return
		}
		m.startManaged(NewWireGuardConnector(vpnCfg.WireGuard))

	case "openvpn":
		if vpnCfg.OpenVPN == nil {
			log.Println("VPN protocol set to openvpn but no openvpn config found, falling back to passive mode")
			m.startPassive(vpnCfg.Interface)
			return
		}
		m.startManaged(NewOpenVPNConnector(vpnCfg.OpenVPN))

	default:
		// Legacy/passive mode — just monitor an externally-managed interface
		m.startPassive(vpnCfg.Interface)
	}
}

func (m *Manager) startPassive(interfaceName string) {
	m.mu.Lock()
	m.managed = false
	m.connector = nil
	m.monitor = NewMonitor(interfaceName)
	m.mu.Unlock()

	m.monitor.OnDown(func() {
		m.mu.RLock()
		fn := m.onDown
		m.mu.RUnlock()
		if fn != nil {
			fn()
		}
	})
	m.monitor.OnUp(func() {
		m.mu.RLock()
		fn := m.onUp
		m.mu.RUnlock()
		if fn != nil {
			fn(interfaceName)
		}
	})
	m.monitor.Start()

	log.Printf("VPN manager started in passive mode (interface: %s)", interfaceName)
}

func (m *Manager) startManaged(conn Connector) {
	m.mu.Lock()
	m.managed = true
	m.connector = conn
	m.mu.Unlock()

	log.Printf("VPN manager starting in managed mode (%T)", conn)

	// Respect the user's last explicit connect/disconnect decision.
	// AutoConnect is nil by default (meaning "do connect"); it is only
	// set to false when the user explicitly clicks Disconnect.
	vpnCfg := m.cfg.GetVPN()
	if vpnCfg.AutoConnect != nil && !*vpnCfg.AutoConnect {
		log.Println("VPN auto-connect disabled (user disconnected last session) — staying disconnected")
		return
	}

	// Attempt initial connection
	if err := conn.Connect(m.ctx); err != nil {
		log.Printf("VPN initial connection failed: %v — will retry", err)
		m.startReconnectLoop()
		return
	}

	ifName := conn.InterfaceName()
	log.Printf("VPN connected, interface: %s", ifName)

	m.startMonitorForManaged(ifName)
}

func (m *Manager) startMonitorForManaged(ifName string) {
	m.mu.Lock()
	m.monitor = NewMonitor(ifName)
	m.mu.Unlock()

	m.monitor.OnDown(func() {
		log.Println("VPN interface went down — pausing and attempting reconnect")
		m.mu.RLock()
		fn := m.onDown
		m.mu.RUnlock()
		if fn != nil {
			fn()
		}
		m.startReconnectLoop()
	})
	m.monitor.OnUp(func() {
		m.mu.RLock()
		fn := m.onUp
		m.mu.RUnlock()
		if fn != nil {
			fn(ifName)
		}
	})
	m.monitor.Start()
	// Monitor's initial checkInterface() will fire onUp if the interface is already up.
}

func (m *Manager) startReconnectLoop() {
	m.reconnectMu.Lock()
	if m.reconnecting {
		m.reconnectMu.Unlock()
		return
	}
	m.reconnecting = true
	m.reconnectMu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.reconnectMu.Lock()
			m.reconnecting = false
			m.reconnectMu.Unlock()
		}()

		const maxAttempts = 10
		backoff := 5 * time.Second
		const maxBackoff = 60 * time.Second

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(backoff):
			}

			log.Printf("VPN reconnect attempt %d/%d", attempt, maxAttempts)

			m.mu.RLock()
			conn := m.connector
			m.mu.RUnlock()
			if conn == nil {
				return
			}

			// Disconnect any stale state first
			conn.Disconnect()

			if err := conn.Connect(m.ctx); err != nil {
				log.Printf("VPN reconnect failed: %v", err)
				backoff = backoff * 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			ifName := conn.InterfaceName()
			log.Printf("VPN reconnected, interface: %s", ifName)

			// Stop old monitor if any
			m.mu.RLock()
			oldMon := m.monitor
			m.mu.RUnlock()
			if oldMon != nil {
				oldMon.Stop()
			}

			m.startMonitorForManaged(ifName)
			return
		}

		log.Printf("VPN reconnect failed after %d attempts — giving up", maxAttempts)
	}()
}

// Stop tears down the manager, disconnecting if in managed mode.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	// Wait for the reconnect loop to exit before tearing down the connection.
	// Without this, the loop can race to call conn.Connect after we Disconnect,
	// leaving a partially-created interface behind.
	m.wg.Wait()

	m.mu.RLock()
	mon := m.monitor
	conn := m.connector
	managed := m.managed
	m.mu.RUnlock()

	if mon != nil {
		mon.Stop()
	}
	if managed && conn != nil {
		if err := conn.Disconnect(); err != nil {
			log.Printf("VPN disconnect error: %v", err)
		}
	}
}

// IsUp returns whether the VPN interface is currently up.
func (m *Manager) IsUp() bool {
	m.mu.RLock()
	mon := m.monitor
	m.mu.RUnlock()
	if mon != nil {
		return mon.IsUp()
	}
	return false
}

// InterfaceName returns the current VPN interface name.
func (m *Manager) InterfaceName() string {
	m.mu.RLock()
	conn := m.connector
	mon := m.monitor
	managed := m.managed
	m.mu.RUnlock()

	if managed && conn != nil {
		return conn.InterfaceName()
	}
	if mon != nil {
		return mon.InterfaceName()
	}
	return ""
}

// Connect initiates a managed VPN connection. Only valid in managed mode.
func (m *Manager) Connect() error {
	m.mu.RLock()
	conn := m.connector
	managed := m.managed
	m.mu.RUnlock()

	if !managed || conn == nil {
		return nil
	}

	if err := conn.Connect(m.ctx); err != nil {
		return err
	}

	ifName := conn.InterfaceName()

	// Stop old monitor
	m.mu.RLock()
	oldMon := m.monitor
	m.mu.RUnlock()
	if oldMon != nil {
		oldMon.Stop()
	}

	m.startMonitorForManaged(ifName)
	return nil
}

// Disconnect tears down a managed VPN connection.
func (m *Manager) Disconnect() error {
	m.mu.RLock()
	conn := m.connector
	mon := m.monitor
	managed := m.managed
	m.mu.RUnlock()

	if !managed || conn == nil {
		return nil
	}

	if mon != nil {
		mon.Stop()
	}

	return conn.Disconnect()
}

// ConnectorStatus returns the status of the managed connector, or a
// synthesized status for passive mode.
func (m *Manager) ConnectorStatus() ConnectorStatus {
	m.mu.RLock()
	conn := m.connector
	managed := m.managed
	mon := m.monitor
	m.mu.RUnlock()

	if managed && conn != nil {
		return conn.Status()
	}

	// Passive mode — synthesize from monitor
	if mon != nil {
		state := StateDisconnected
		if mon.IsUp() {
			state = StateConnected
		}
		return ConnectorStatus{
			State:         state,
			InterfaceName: mon.InterfaceName(),
		}
	}

	return ConnectorStatus{State: StateDisconnected}
}

// IsManaged returns true if the manager is in managed mode (owns VPN connection).
func (m *Manager) IsManaged() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.managed
}

// Reconfigure tears down the current VPN and restarts with new config.
// Call this after updating cfg.VPN via the API.
func (m *Manager) Reconfigure() {
	m.reconfigureMu.Lock()
	defer m.reconfigureMu.Unlock()

	m.Stop()
	m.wg = sync.WaitGroup{}
	m.Start(context.Background())
}

// SetPassiveInterface updates the interface for passive mode at runtime.
// Used when the user changes just the interface name via the legacy API.
func (m *Manager) SetPassiveInterface(name string) {
	m.mu.RLock()
	mon := m.monitor
	m.mu.RUnlock()
	if mon != nil {
		mon.SetInterface(name)
	}
}
