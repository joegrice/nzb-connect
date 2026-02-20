package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// RealUID/RealGID return the UID and GID of the user who actually invoked the
// process.  When running under sudo, these come from SUDO_UID/SUDO_GID rather
// than the effective (root) UID — so files we create can be chowned back to
// the real user and they can manage them without root.
func RealUID() int {
	if s := os.Getenv("SUDO_UID"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return os.Getuid()
}

func RealGID() int {
	if s := os.Getenv("SUDO_GID"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return os.Getgid()
}

// ChownToRealUser recursively chowns path to the real (non-sudo) user so that
// files created by a root process are accessible without elevated privileges.
func ChownToRealUser(path string) {
	uid, gid := RealUID(), RealGID()
	if uid == 0 {
		return // already running as non-root, nothing to do
	}
	filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			os.Chown(p, uid, gid)
		}
		return nil
	})
}

// expandPath expands a leading ~ to the real user's home directory.
// When running via sudo, ~ resolves to the invoking user's home, not /root.
func expandPath(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home := realHome()
	if home == "" {
		return path
	}
	return home + path[1:]
}

// realHome returns the home directory of the real (non-sudo) user.
func realHome() string {
	// Prefer SUDO_USER so that "sudo ./nzb-connect" resolves to /home/joe, not /root
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil {
			return u.HomeDir
		}
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

type Config struct {
	mu          sync.RWMutex
	filePath    string
	VPN         VPNConfig         `yaml:"vpn"`
	Servers     []ServerConfig    `yaml:"servers"`
	Paths       PathsConfig       `yaml:"paths"`
	Web         WebConfig         `yaml:"web"`
	PostProcess PostProcessConfig `yaml:"postprocess"`
}

type VPNConfig struct {
	Enabled   bool             `yaml:"enabled" json:"enabled"`
	Protocol  string           `yaml:"protocol" json:"protocol"`   // "wireguard", "openvpn", or "" (passive/legacy)
	Interface string           `yaml:"interface" json:"interface"` // legacy passive mode only
	WireGuard *WireGuardConfig `yaml:"wireguard,omitempty" json:"wireguard,omitempty"`
	OpenVPN   *OpenVPNConfig   `yaml:"openvpn,omitempty" json:"openvpn,omitempty"`
}

type WireGuardConfig struct {
	PrivateKey          string `yaml:"private_key" json:"private_key"`
	Address             string `yaml:"address" json:"address"`
	DNS                 string `yaml:"dns,omitempty" json:"dns,omitempty"`
	ListenPort          int    `yaml:"listen_port,omitempty" json:"listen_port,omitempty"`
	PeerPublicKey       string `yaml:"peer_public_key" json:"peer_public_key"`
	PeerEndpoint        string `yaml:"peer_endpoint" json:"peer_endpoint"`
	PresharedKey        string `yaml:"preshared_key,omitempty" json:"preshared_key,omitempty"`
	AllowedIPs          string `yaml:"allowed_ips,omitempty" json:"allowed_ips,omitempty"`
	PersistentKeepalive int    `yaml:"persistent_keepalive,omitempty" json:"persistent_keepalive,omitempty"`
}

type OpenVPNConfig struct {
	RemoteHost string `yaml:"remote_host" json:"remote_host"`
	RemotePort int    `yaml:"remote_port,omitempty" json:"remote_port,omitempty"`
	Protocol   string `yaml:"protocol,omitempty" json:"protocol,omitempty"` // "udp" or "tcp"
	AuthType   string `yaml:"auth_type,omitempty" json:"auth_type,omitempty"` // "userpass" or "certificate"
	Username   string `yaml:"username,omitempty" json:"username,omitempty"`
	Password   string `yaml:"password,omitempty" json:"password,omitempty"`
	CACert     string `yaml:"ca_cert,omitempty" json:"ca_cert,omitempty"`
	ClientCert string `yaml:"client_cert,omitempty" json:"client_cert,omitempty"`
	ClientKey  string `yaml:"client_key,omitempty" json:"client_key,omitempty"`
	TLSAuth    string `yaml:"tls_auth,omitempty" json:"tls_auth,omitempty"`
	Cipher     string `yaml:"cipher,omitempty" json:"cipher,omitempty"`
	Auth       string `yaml:"auth,omitempty" json:"auth,omitempty"`
	Compress   string `yaml:"compress,omitempty" json:"compress,omitempty"`
	DeviceType string `yaml:"device_type,omitempty" json:"device_type,omitempty"` // "tun" or "tap"
}

type ServerConfig struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Host        string `yaml:"host" json:"host"`
	Port        int    `yaml:"port" json:"port"`
	SSL         bool   `yaml:"ssl" json:"ssl"`
	Username    string `yaml:"username" json:"username"`
	Password    string `yaml:"password" json:"password"`
	Connections int    `yaml:"connections" json:"connections"`
	Enabled     bool   `yaml:"enabled" json:"enabled"`
}

type PathsConfig struct {
	Incomplete string `yaml:"incomplete"`
	Complete   string `yaml:"complete"`
	Temp       string `yaml:"temp"`
}

type WebConfig struct {
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type PostProcessConfig struct {
	Unrar          string `yaml:"unrar"`
	SevenZip       string `yaml:"sevenzip"`
	DeleteArchives bool   `yaml:"delete_archives"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{filePath: path}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	cfg.setDefaults()
	return cfg, nil
}

func (c *Config) setDefaults() {
	if c.Web.Port == 0 {
		c.Web.Port = 6789
	}

	// Expand ~ in paths before applying defaults so that ~/Downloads works
	// whether set explicitly in config.yaml or resolved below.
	c.Paths.Incomplete = expandPath(c.Paths.Incomplete)
	c.Paths.Complete = expandPath(c.Paths.Complete)
	c.Paths.Temp = expandPath(c.Paths.Temp)

	if c.Paths.Incomplete == "" {
		c.Paths.Incomplete = filepath.Join(realHome(), "Downloads", "nzb-connect", "incomplete")
		if realHome() == "" {
			c.Paths.Incomplete = "/downloads/incomplete"
		}
	}
	if c.Paths.Complete == "" {
		c.Paths.Complete = filepath.Join(realHome(), "Downloads", "nzb-connect", "complete")
		if realHome() == "" {
			c.Paths.Complete = "/downloads/complete"
		}
	}
	if c.Paths.Temp == "" {
		c.Paths.Temp = filepath.Join(realHome(), ".cache", "nzb-connect", "tmp")
		if realHome() == "" {
			c.Paths.Temp = "/tmp/nzb-connect"
		}
	}
	for i := range c.Servers {
		if c.Servers[i].Connections == 0 {
			c.Servers[i].Connections = 10
		}
		if c.Servers[i].Port == 0 {
			if c.Servers[i].SSL {
				c.Servers[i].Port = 563
			} else {
				c.Servers[i].Port = 119
			}
		}
	}
}

func (c *Config) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	if err := os.WriteFile(c.filePath, data, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	return nil
}

func (c *Config) GetVPN() VPNConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.VPN
}

func (c *Config) SetVPN(vpn VPNConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.VPN = vpn
}

func (c *Config) GetServers() []ServerConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ServerConfig, len(c.Servers))
	copy(result, c.Servers)
	return result
}

func (c *Config) AddServer(srv ServerConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Servers = append(c.Servers, srv)
}

func (c *Config) UpdateServer(id string, srv ServerConfig) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.Servers {
		if s.ID == id || s.Name == id {
			srv.ID = s.ID
			c.Servers[i] = srv
			return true
		}
	}
	return false
}

func (c *Config) DeleteServer(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.Servers {
		if s.ID == id || s.Name == id {
			c.Servers = append(c.Servers[:i], c.Servers[i+1:]...)
			return true
		}
	}
	return false
}

func (c *Config) EnsureDirectories() error {
	dirs := []string{c.Paths.Incomplete, c.Paths.Complete, c.Paths.Temp}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
		ChownToRealUser(dir)
	}
	return nil
}
