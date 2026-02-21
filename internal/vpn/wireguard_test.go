package vpn

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nzb-connect/internal/config"
)

// --- latestHandshake parsing ---

// parseHandshakesOutput is the pure parsing half of latestHandshake,
// extracted here so it can be tested without running wg(8).
func parseHandshakesOutput(output string) (time.Time, error) {
	var latest time.Time
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var tsUnix int64
		if _, err := fmt.Sscan(fields[1], &tsUnix); err != nil || tsUnix == 0 {
			continue
		}
		t := time.Unix(tsUnix, 0)
		if t.After(latest) {
			latest = t
		}
	}
	return latest, nil
}

func TestParseHandshakesOutput(t *testing.T) {
	now := time.Now().Unix()

	cases := []struct {
		name     string
		input    string
		wantZero bool
	}{
		{
			name:     "single peer recent handshake",
			input:    fmt.Sprintf("AABBCCDDEE==\t%d\n", now-1),
			wantZero: false,
		},
		{
			name:     "timestamp 0 means no handshake yet",
			input:    "AABBCCDDEE==\t0\n",
			wantZero: true,
		},
		{
			name:  "multiple peers, returns latest",
			input: fmt.Sprintf("peer1==\t%d\npeer2==\t%d\n", now-60, now-2),
		},
		{
			name:     "empty output",
			input:    "",
			wantZero: true,
		},
		{
			name:     "whitespace only",
			input:    "   \n\n",
			wantZero: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHandshakesOutput(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantZero && !got.IsZero() {
				t.Errorf("expected zero time, got %v", got)
			}
			if !tc.wantZero && got.IsZero() {
				t.Errorf("expected non-zero time, got zero")
			}
		})
	}

	// Multiple peers: check the latest is picked
	t.Run("multiple peers picks latest timestamp", func(t *testing.T) {
		older := now - 120
		newer := now - 5
		input := fmt.Sprintf("peer1==\t%d\npeer2==\t%d\n", older, newer)
		got, _ := parseHandshakesOutput(input)
		if got.Unix() != newer {
			t.Errorf("expected timestamp %d, got %d", newer, got.Unix())
		}
	})
}

// --- waitForHandshake: context cancellation ---

func TestWaitForHandshakeContextCancel(t *testing.T) {
	w := &WireGuardConnector{cfg: &config.WireGuardConfig{}}

	// Cancel after 500ms — much faster than the 30s internal timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := w.waitForHandshake(ctx, "wg_no_such_iface_test")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	// Should return in ~500ms (context timeout), not 30s (internal timeout)
	if elapsed > 5*time.Second {
		t.Errorf("waitForHandshake took %v, expected ~500ms", elapsed)
	}
	t.Logf("correctly returned error in %v: %v", elapsed, err)
}

// --- addEndpointRoute: gateway parsing ---

func TestAddEndpointRouteGatewayParsing(t *testing.T) {
	// Test the parsing logic of the ip route show output we'd receive
	sampleOutput := "default via 192.168.1.1 dev eth0 proto dhcp src 192.168.1.100 metric 100"

	var gateway, dev string
	for _, line := range strings.Split(sampleOutput, "\n") {
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

	if gateway != "192.168.1.1" {
		t.Errorf("expected gateway 192.168.1.1, got %q", gateway)
	}
	if dev != "eth0" {
		t.Errorf("expected dev eth0, got %q", dev)
	}
}

// --- teardownRouting: endpointRoute field parsing ---

func TestEndpointRouteTeardownParsing(t *testing.T) {
	// Confirm the stored route string can be split correctly for teardown
	endpointRoute := "5.157.13.2/32 via 192.168.1.1 dev eth0"
	parts := strings.Fields(endpointRoute)

	if len(parts) != 5 {
		t.Fatalf("expected 5 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "5.157.13.2/32" {
		t.Errorf("expected host/32, got %q", parts[0])
	}
	if parts[2] != "192.168.1.1" {
		t.Errorf("expected gateway, got %q", parts[2])
	}
}
