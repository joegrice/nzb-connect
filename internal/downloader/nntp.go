package downloader

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joe/nzb-connect/internal/config"
	"github.com/joe/nzb-connect/internal/vpn"
)

// NNTPConn represents a single NNTP connection.
type NNTPConn struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	server config.ServerConfig
}

// Connect establishes an NNTP connection, optionally through a VPN-bound dialer.
func Connect(ctx context.Context, server config.ServerConfig, vpnInterface string) (*NNTPConn, error) {
	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)

	var conn net.Conn
	var err error

	if server.SSL {
		tlsConfig := &tls.Config{
			ServerName: server.Host,
		}
		if vpnInterface != "" {
			dialer := vpn.BindToInterface(vpnInterface)
			netConn, dialErr := dialer.DialContext(ctx, "tcp", addr)
			if dialErr != nil {
				return nil, fmt.Errorf("dial %s: %w", addr, dialErr)
			}
			conn = tls.Client(netConn, tlsConfig)
			if err := conn.(*tls.Conn).HandshakeContext(ctx); err != nil {
				netConn.Close()
				return nil, fmt.Errorf("TLS handshake: %w", err)
			}
		} else {
			dialer := &tls.Dialer{Config: tlsConfig}
			conn, err = dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, fmt.Errorf("TLS dial %s: %w", addr, err)
			}
		}
	} else {
		if vpnInterface != "" {
			dialer := vpn.BindToInterface(vpnInterface)
			conn, err = dialer.DialContext(ctx, "tcp", addr)
		} else {
			var d net.Dialer
			conn, err = d.DialContext(ctx, "tcp", addr)
		}
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
	}

	nc := &NNTPConn{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		server: server,
	}

	// Read welcome banner
	code, _, err := nc.readResponse()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading welcome: %w", err)
	}
	if code != 200 && code != 201 {
		conn.Close()
		return nil, fmt.Errorf("unexpected welcome code: %d", code)
	}

	// Authenticate
	if server.Username != "" {
		if err := nc.authenticate(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("auth: %w", err)
		}
	}

	return nc, nil
}

func (nc *NNTPConn) sendCommand(cmd string) error {
	nc.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err := nc.writer.WriteString(cmd + "\r\n")
	if err != nil {
		return err
	}
	return nc.writer.Flush()
}

func (nc *NNTPConn) readResponse() (int, string, error) {
	nc.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	line, err := nc.reader.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 3 {
		return 0, "", fmt.Errorf("short response: %q", line)
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, "", fmt.Errorf("invalid response code: %q", line[:3])
	}
	msg := ""
	if len(line) > 4 {
		msg = line[4:]
	}
	return code, msg, nil
}

// readMultiLine reads a dot-terminated multi-line response body.
func (nc *NNTPConn) readMultiLine() ([]byte, error) {
	var buf []byte
	for {
		nc.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		line, err := nc.reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		// Check for termination dot
		trimmed := strings.TrimRight(string(line), "\r\n")
		if trimmed == "." {
			break
		}
		// Handle dot-stuffing: lines starting with ".." become "."
		if strings.HasPrefix(trimmed, "..") {
			line = append([]byte("."), line[2:]...)
		}
		buf = append(buf, line...)
	}
	return buf, nil
}

func (nc *NNTPConn) authenticate() error {
	if err := nc.sendCommand("AUTHINFO USER " + nc.server.Username); err != nil {
		return fmt.Errorf("sending username: %w", err)
	}
	code, _, err := nc.readResponse()
	if err != nil {
		return fmt.Errorf("reading user response: %w", err)
	}
	if code == 281 {
		return nil // No password needed
	}
	if code != 381 {
		return fmt.Errorf("unexpected user response: %d", code)
	}

	if err := nc.sendCommand("AUTHINFO PASS " + nc.server.Password); err != nil {
		return fmt.Errorf("sending password: %w", err)
	}
	code, _, err = nc.readResponse()
	if err != nil {
		return fmt.Errorf("reading pass response: %w", err)
	}
	if code != 281 {
		return fmt.Errorf("authentication failed: %d", code)
	}
	return nil
}

// FetchBody fetches the body of an article by message ID.
func (nc *NNTPConn) FetchBody(messageID string) ([]byte, error) {
	// Ensure message ID is wrapped in angle brackets
	if !strings.HasPrefix(messageID, "<") {
		messageID = "<" + messageID + ">"
	}

	if err := nc.sendCommand("BODY " + messageID); err != nil {
		return nil, fmt.Errorf("sending BODY: %w", err)
	}

	code, _, err := nc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("reading BODY response: %w", err)
	}
	if code != 222 {
		return nil, fmt.Errorf("BODY failed with code %d", code)
	}

	return nc.readMultiLine()
}

// Close closes the NNTP connection.
func (nc *NNTPConn) Close() error {
	nc.sendCommand("QUIT")
	return nc.conn.Close()
}

// ConnectionPool manages a pool of NNTP connections to a server.
type ConnectionPool struct {
	server       config.ServerConfig
	vpnInterface string
	maxConns     int
	mu           sync.Mutex
	conns        chan *NNTPConn
	active       int
}

// NewConnectionPool creates a new pool for the given server.
func NewConnectionPool(server config.ServerConfig, vpnInterface string) *ConnectionPool {
	maxConns := server.Connections
	if maxConns <= 0 {
		maxConns = 10
	}
	if maxConns > 50 {
		maxConns = 50
	}
	return &ConnectionPool{
		server:       server,
		vpnInterface: vpnInterface,
		maxConns:     maxConns,
		conns:        make(chan *NNTPConn, maxConns),
	}
}

// Get retrieves a connection from the pool or creates a new one.
func (p *ConnectionPool) Get(ctx context.Context) (*NNTPConn, error) {
	// Try to get an existing connection
	select {
	case conn := <-p.conns:
		return conn, nil
	default:
	}

	// Create a new connection if under the limit
	p.mu.Lock()
	if p.active >= p.maxConns {
		p.mu.Unlock()
		// Wait for one to be returned
		select {
		case conn := <-p.conns:
			return conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.active++
	p.mu.Unlock()

	conn, err := Connect(ctx, p.server, p.vpnInterface)
	if err != nil {
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
		return nil, err
	}
	return conn, nil
}

// Put returns a connection to the pool.
func (p *ConnectionPool) Put(conn *NNTPConn) {
	select {
	case p.conns <- conn:
	default:
		// Pool is full, close connection
		conn.Close()
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
	}
}

// Discard removes a broken connection from the pool count.
func (p *ConnectionPool) Discard(conn *NNTPConn) {
	conn.Close()
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
}

// Close closes all connections in the pool.
func (p *ConnectionPool) Close() {
	close(p.conns)
	for conn := range p.conns {
		conn.Close()
	}
}

// TestConnection tests connectivity to an NNTP server.
func TestConnection(ctx context.Context, server config.ServerConfig, vpnInterface string) error {
	conn, err := Connect(ctx, server, vpnInterface)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// PoolManager manages connection pools for multiple servers.
type PoolManager struct {
	mu           sync.RWMutex
	pools        map[string]*ConnectionPool
	vpnInterface string
}

// NewPoolManager creates a new pool manager.
func NewPoolManager(vpnInterface string) *PoolManager {
	return &PoolManager{
		pools:        make(map[string]*ConnectionPool),
		vpnInterface: vpnInterface,
	}
}

// UpdateServers reconfigures pools based on the current server list.
func (pm *PoolManager) UpdateServers(servers []config.ServerConfig) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Close pools for removed servers
	newNames := make(map[string]bool)
	for _, s := range servers {
		if s.Enabled {
			newNames[s.Name] = true
		}
	}
	for name, pool := range pm.pools {
		if !newNames[name] {
			pool.Close()
			delete(pm.pools, name)
		}
	}

	// Add/update pools
	for _, s := range servers {
		if !s.Enabled {
			continue
		}
		if _, exists := pm.pools[s.Name]; !exists {
			pm.pools[s.Name] = NewConnectionPool(s, pm.vpnInterface)
			log.Printf("Created connection pool for server %s (%d connections)", s.Name, s.Connections)
		}
	}
}

// GetConnection gets a connection from any available server.
func (pm *PoolManager) GetConnection(ctx context.Context) (*NNTPConn, *ConnectionPool, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, pool := range pm.pools {
		conn, err := pool.Get(ctx)
		if err != nil {
			log.Printf("Failed to get connection from pool: %v", err)
			continue
		}
		return conn, pool, nil
	}
	return nil, nil, fmt.Errorf("no NNTP connections available")
}

// FetchSegment fetches a segment from any available server with retries.
func (pm *PoolManager) FetchSegment(ctx context.Context, messageID string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			select {
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		conn, pool, err := pm.GetConnection(ctx)
		if err != nil {
			lastErr = err
			continue
		}

		data, err := conn.FetchBody(messageID)
		if err != nil {
			pool.Discard(conn)
			lastErr = fmt.Errorf("fetch body: %w", err)
			continue
		}

		pool.Put(conn)
		return data, nil
	}
	return nil, fmt.Errorf("all retries failed for %s: %w", messageID, lastErr)
}

// SetVPNInterface changes the VPN interface used for new connections.
// Existing connections are closed and pools are reset.
func (pm *PoolManager) SetVPNInterface(iface string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.vpnInterface = iface
	for _, pool := range pm.pools {
		pool.Close()
	}
	pm.pools = make(map[string]*ConnectionPool)
	log.Printf("Pool manager VPN interface updated to: %s", iface)
}

// CloseAll closes all connection pools.
func (pm *PoolManager) CloseAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for _, pool := range pm.pools {
		pool.Close()
	}
	pm.pools = make(map[string]*ConnectionPool)
}

// Ensure NNTPConn implements io.Closer
var _ io.Closer = (*NNTPConn)(nil)
