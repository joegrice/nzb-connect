package vpn

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"syscall"
	"time"
)

// Monitor watches a network interface and reports its status.
type Monitor struct {
	interfaceName string
	mu            sync.RWMutex
	isUp          bool
	onDown        func()
	onUp          func()
	stopCh        chan struct{}
	stopOnce      sync.Once
}

// NewMonitor creates a new VPN interface monitor.
func NewMonitor(interfaceName string) *Monitor {
	return &Monitor{
		interfaceName: interfaceName,
		stopCh:        make(chan struct{}),
	}
}

// OnDown sets a callback for when the interface goes down.
func (m *Monitor) OnDown(fn func()) {
	m.onDown = fn
}

// OnUp sets a callback for when the interface comes up.
func (m *Monitor) OnUp(fn func()) {
	m.onUp = fn
}

// IsUp returns whether the interface is currently up.
func (m *Monitor) IsUp() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isUp
}

// InterfaceName returns the monitored interface name.
func (m *Monitor) InterfaceName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interfaceName
}

// SetInterface changes the monitored interface name at runtime.
func (m *Monitor) SetInterface(name string) {
	m.mu.Lock()
	m.interfaceName = name
	m.mu.Unlock()
	log.Printf("VPN interface changed to %s", name)
	m.checkInterface()
}

// Start begins monitoring the interface. It checks every 2 seconds.
func (m *Monitor) Start() {
	// Do initial check
	m.checkInterface()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.checkInterface()
			case <-m.stopCh:
				return
			}
		}
	}()
}

// Stop stops the interface monitor. Safe to call multiple times.
func (m *Monitor) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

func (m *Monitor) checkInterface() {
	m.mu.RLock()
	name := m.interfaceName
	m.mu.RUnlock()

	iface, err := net.InterfaceByName(name)
	up := err == nil && iface.Flags&net.FlagUp != 0

	m.mu.Lock()
	wasUp := m.isUp
	m.isUp = up
	m.mu.Unlock()

	if wasUp && !up {
		log.Printf("VPN interface %s went DOWN", name)
		if m.onDown != nil {
			m.onDown()
		}
	} else if !wasUp && up {
		log.Printf("VPN interface %s is UP", name)
		if m.onUp != nil {
			m.onUp()
		}
	}
}

// BindToInterface creates a net.Dialer that binds all connections to the
// specified network interface using SO_BINDTODEVICE.
func BindToInterface(interfaceName string) *net.Dialer {
	return &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			var bindErr error
			err := c.Control(func(fd uintptr) {
				bindErr = syscall.SetsockoptString(
					int(fd),
					syscall.SOL_SOCKET,
					syscall.SO_BINDTODEVICE,
					interfaceName,
				)
			})
			if err != nil {
				return fmt.Errorf("raw conn control: %w", err)
			}
			if bindErr != nil {
				return fmt.Errorf("SO_BINDTODEVICE to %s: %w", interfaceName, bindErr)
			}
			return nil
		},
	}
}

// DialContext creates a connection bound to the VPN interface.
func DialContext(ctx context.Context, interfaceName, network, address string) (net.Conn, error) {
	dialer := BindToInterface(interfaceName)
	return dialer.DialContext(ctx, network, address)
}
