package vpn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/joe/nzb-connect/internal/config"
)

// TestOpenVPNWaitsForInitSequence verifies the fix for Bug 5:
// Connect() must only return success on "Initialization Sequence Completed",
// NOT on "TUN/TAP device ... opened" (which happens much earlier in the handshake).
//
// We use a fake openvpn binary (a shell script) that emits the two log lines
// with a 200ms gap so the test can observe which one triggers the signal.
func TestOpenVPNWaitsForInitSequence(t *testing.T) {
	// Write a fake openvpn script to a temp dir
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "openvpn")

	script := `#!/bin/sh
echo "TUN/TAP device tun99 opened"
sleep 0.2
echo "Initialization Sequence Completed"
sleep 10
`
	if err := os.WriteFile(fakePath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// Prepend the temp dir so our fake binary is found first
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	// Also make resolveCmd aware by clearing any cached LookPath
	if _, err := exec.LookPath("openvpn"); err != nil {
		t.Skip("fake openvpn binary not found in PATH (env issue)")
	}

	cfg := &config.OpenVPNConfig{
		RemoteHost: "127.0.0.1",
		RemotePort: 1194,
		Protocol:   "udp",
	}
	conn := NewOpenVPNConnector(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := conn.Connect(ctx)
	elapsed := time.Since(start)

	// The fake binary outputs "Initialization Sequence Completed" after ~200ms.
	// If the old bug were present, Connect() would return after ~0ms (on "TUN/TAP device").
	if err != nil {
		t.Fatalf("Connect() returned error: %v", err)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("Connect() returned in %v — looks like it triggered on TUN/TAP device line, not Initialization Sequence Completed", elapsed)
	}
	if conn.InterfaceName() != "tun99" {
		t.Errorf("expected interface tun99, got %q", conn.InterfaceName())
	}

	conn.Disconnect()
	t.Logf("Connect() returned after %v with interface %s", elapsed, conn.InterfaceName())
}

// TestOpenVPNAuthFailure confirms AUTH_FAILED triggers an error.
func TestOpenVPNAuthFailure(t *testing.T) {
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "openvpn")

	script := `#!/bin/sh
echo "TUN/TAP device tun0 opened"
sleep 0.05
echo "AUTH_FAILED"
sleep 10
`
	if err := os.WriteFile(fakePath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	cfg := &config.OpenVPNConfig{
		RemoteHost: "127.0.0.1",
		RemotePort: 1194,
	}
	conn := NewOpenVPNConnector(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := conn.Connect(ctx)
	if err == nil {
		t.Fatal("expected error on AUTH_FAILED, got nil")
	}
	if conn.Status().State != StateError {
		t.Errorf("expected state %q, got %q", StateError, conn.Status().State)
	}
	t.Logf("correctly received error: %v", err)
}
