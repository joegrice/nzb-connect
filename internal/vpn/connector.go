package vpn

import (
	"context"
	"os/exec"
	"time"
)

// Connector state constants.
const (
	StateDisconnected = "disconnected"
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateError        = "error"
	StateReconnecting = "reconnecting"
)

// ConnectorStatus represents the current state of a VPN connector.
type ConnectorStatus struct {
	State         string    `json:"state"`
	InterfaceName string    `json:"interface_name,omitempty"`
	Error         string    `json:"error,omitempty"`
	ConnectedAt   time.Time `json:"connected_at,omitempty"`
}

// resolveCmd finds a command in $PATH or common sbin directories.
// Many systems don't include /usr/sbin or /sbin in $PATH for non-login
// shells, but that's where ip, wg, and openvpn typically live.
func resolveCmd(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin", "/usr/bin", "/usr/local/bin"} {
		candidate := dir + "/" + name
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	return name // fall back to bare name, let exec fail with a clear error
}

// Connector defines the interface for managed VPN connections.
type Connector interface {
	// Connect establishes the VPN tunnel and returns when connected or on error.
	Connect(ctx context.Context) error

	// Disconnect tears down the VPN tunnel.
	Disconnect() error

	// Status returns the current connector state.
	Status() ConnectorStatus

	// InterfaceName returns the name of the created network interface.
	InterfaceName() string
}
