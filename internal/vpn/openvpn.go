package vpn

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/joe/nzb-connect/internal/config"
)

// OpenVPNConnector manages an OpenVPN tunnel by spawning the openvpn process.
type OpenVPNConnector struct {
	cfg *config.OpenVPNConfig

	mu     sync.RWMutex
	status ConnectorStatus
	ifName string

	cmd       *exec.Cmd
	cancel    context.CancelFunc
	tempFiles []string
}

// NewOpenVPNConnector creates a new OpenVPN connector.
func NewOpenVPNConnector(cfg *config.OpenVPNConfig) *OpenVPNConnector {
	return &OpenVPNConnector{
		cfg:    cfg,
		status: ConnectorStatus{State: StateDisconnected},
	}
}

// Connect spawns the openvpn process and waits for the tunnel to come up.
func (o *OpenVPNConnector) Connect(ctx context.Context) error {
	o.mu.Lock()
	o.status = ConnectorStatus{State: StateConnecting}
	o.mu.Unlock()

	args, tempFiles, err := o.buildArgs()
	if err != nil {
		o.setError(err)
		return err
	}

	o.mu.Lock()
	o.tempFiles = tempFiles
	o.mu.Unlock()

	procCtx, procCancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(procCtx, resolveCmd("openvpn"), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		procCancel()
		o.cleanupTempFiles()
		o.setError(fmt.Errorf("stdout pipe: %w", err))
		return err
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		procCancel()
		o.cleanupTempFiles()
		o.setError(fmt.Errorf("start openvpn: %w", err))
		return err
	}

	o.mu.Lock()
	o.cmd = cmd
	o.cancel = procCancel
	o.mu.Unlock()

	// Parse output looking for connection state
	connected := make(chan string, 1) // sends interface name on connect
	connErr := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		var capturedIfName string
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[openvpn] %s", line)

			// Capture TUN/TAP device name but don't signal yet — the server
			// hasn't finished pushing routes and config at this point.
			if strings.Contains(line, "TUN/TAP device") && strings.Contains(line, "opened") {
				// e.g. "TUN/TAP device tun0 opened"
				parts := strings.Fields(line)
				for i, p := range parts {
					if p == "device" && i+1 < len(parts) {
						capturedIfName = parts[i+1]
						break
					}
				}
			}

			// Detect auth failure
			if strings.Contains(line, "AUTH_FAILED") {
				select {
				case connErr <- fmt.Errorf("authentication failed"):
				default:
				}
			}

			// Signal connected only when OpenVPN has fully completed
			// initialisation (routes and DNS pushed from server).
			if strings.Contains(line, "Initialization Sequence Completed") {
				ifName := capturedIfName
				if ifName == "" {
					ifName = o.defaultDevName()
				}
				select {
				case connected <- ifName:
				default:
				}
			}
		}
	}()

	// Also watch for process exit
	go func() {
		if err := cmd.Wait(); err != nil {
			select {
			case connErr <- fmt.Errorf("openvpn exited: %w", err):
			default:
			}
		}
	}()

	// Wait for connection or failure with timeout
	timeout := time.After(60 * time.Second)
	select {
	case ifName := <-connected:
		o.mu.Lock()
		o.ifName = ifName
		o.status = ConnectorStatus{
			State:         StateConnected,
			InterfaceName: ifName,
			ConnectedAt:   time.Now(),
		}
		o.mu.Unlock()
		log.Printf("OpenVPN connected, interface: %s", ifName)
		return nil

	case err := <-connErr:
		o.setError(err)
		o.killProcess()
		return err

	case <-timeout:
		err := fmt.Errorf("openvpn connection timed out after 60s")
		o.setError(err)
		o.killProcess()
		return err

	case <-ctx.Done():
		o.killProcess()
		return ctx.Err()
	}
}

// Disconnect stops the openvpn process.
func (o *OpenVPNConnector) Disconnect() error {
	o.killProcess()
	o.cleanupTempFiles()

	o.mu.Lock()
	o.ifName = ""
	o.status = ConnectorStatus{State: StateDisconnected}
	o.mu.Unlock()

	return nil
}

// Status returns the current connector status.
func (o *OpenVPNConnector) Status() ConnectorStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.status
}

// InterfaceName returns the tunnel interface name.
func (o *OpenVPNConnector) InterfaceName() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.ifName
}

func (o *OpenVPNConnector) buildArgs() ([]string, []string, error) {
	var args []string
	var tempFiles []string

	args = append(args, "--client", "--nobind")

	// Remote
	if o.cfg.RemoteHost == "" {
		return nil, nil, fmt.Errorf("remote_host is required")
	}
	port := o.cfg.RemotePort
	if port == 0 {
		port = 1194
	}
	proto := o.cfg.Protocol
	if proto == "" {
		proto = "udp"
	}
	args = append(args, "--remote", o.cfg.RemoteHost, fmt.Sprintf("%d", port), proto)

	// Device type
	devType := o.cfg.DeviceType
	if devType == "" {
		devType = "tun"
	}
	args = append(args, "--dev", devType)

	// Auth
	if o.cfg.AuthType == "userpass" && o.cfg.Username != "" {
		authFile, err := o.writeTempFile("ovpn-auth-*", o.cfg.Username+"\n"+o.cfg.Password+"\n")
		if err != nil {
			o.cleanupFiles(tempFiles)
			return nil, nil, fmt.Errorf("write auth file: %w", err)
		}
		tempFiles = append(tempFiles, authFile)
		args = append(args, "--auth-user-pass", authFile)
	}

	// Certificates
	if o.cfg.CACert != "" {
		caFile, err := o.writeTempFile("ovpn-ca-*", o.cfg.CACert)
		if err != nil {
			o.cleanupFiles(tempFiles)
			return nil, nil, fmt.Errorf("write ca cert: %w", err)
		}
		tempFiles = append(tempFiles, caFile)
		args = append(args, "--ca", caFile)
	}

	if o.cfg.ClientCert != "" {
		certFile, err := o.writeTempFile("ovpn-cert-*", o.cfg.ClientCert)
		if err != nil {
			o.cleanupFiles(tempFiles)
			return nil, nil, fmt.Errorf("write client cert: %w", err)
		}
		tempFiles = append(tempFiles, certFile)
		args = append(args, "--cert", certFile)
	}

	if o.cfg.ClientKey != "" {
		keyFile, err := o.writeTempFile("ovpn-key-*", o.cfg.ClientKey)
		if err != nil {
			o.cleanupFiles(tempFiles)
			return nil, nil, fmt.Errorf("write client key: %w", err)
		}
		tempFiles = append(tempFiles, keyFile)
		args = append(args, "--key", keyFile)
	}

	if o.cfg.TLSAuth != "" {
		taFile, err := o.writeTempFile("ovpn-ta-*", o.cfg.TLSAuth)
		if err != nil {
			o.cleanupFiles(tempFiles)
			return nil, nil, fmt.Errorf("write tls-auth: %w", err)
		}
		tempFiles = append(tempFiles, taFile)
		args = append(args, "--tls-auth", taFile, "1")
	}

	// Optional parameters
	if o.cfg.Cipher != "" {
		args = append(args, "--cipher", o.cfg.Cipher)
	}
	if o.cfg.Auth != "" {
		args = append(args, "--auth", o.cfg.Auth)
	}
	if o.cfg.Compress != "" {
		args = append(args, "--compress", o.cfg.Compress)
	}

	// Keep output machine-parseable
	args = append(args, "--verb", "3")

	return args, tempFiles, nil
}

func (o *OpenVPNConnector) defaultDevName() string {
	devType := o.cfg.DeviceType
	if devType == "" {
		devType = "tun"
	}
	return devType + "0"
}

func (o *OpenVPNConnector) killProcess() {
	o.mu.Lock()
	cmd := o.cmd
	cancel := o.cancel
	o.cmd = nil
	o.cancel = nil
	o.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		// Give it time to shut down gracefully
		done := make(chan struct{})
		go func() {
			cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			log.Println("OpenVPN process did not exit gracefully, sending SIGKILL")
			cmd.Process.Kill()
		}
	}
}

func (o *OpenVPNConnector) writeTempFile(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(f.Name(), 0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

func (o *OpenVPNConnector) cleanupTempFiles() {
	o.mu.Lock()
	files := o.tempFiles
	o.tempFiles = nil
	o.mu.Unlock()

	o.cleanupFiles(files)
}

func (o *OpenVPNConnector) cleanupFiles(files []string) {
	for _, f := range files {
		os.Remove(f)
	}
}

func (o *OpenVPNConnector) setError(err error) {
	o.mu.Lock()
	o.status = ConnectorStatus{
		State: StateError,
		Error: err.Error(),
	}
	o.mu.Unlock()
	log.Printf("OpenVPN error: %v", err)
}
