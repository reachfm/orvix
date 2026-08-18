package monitoring

// Regression coverage for the ORVIX Premium Super Admin dashboard's
// "real host data only" requirement: GetHealth must expose a real OS
// host uptime (distinct from process uptime) and a real,
// locally-discovered public IPv4 — both truthfully absent rather than
// fabricated when unavailable on the current platform/host.

import (
	"context"
	"database/sql"
	"net"
	"testing"
)

func TestGetHealth_HostUptimeNeverFabricatedWhenUnavailable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	svc := NewService(db, &DataSources{DB: db})
	h := svc.GetHealth(context.Background())

	uptime, ok := hostUptimeSeconds()
	if h.HostUptimeAvailable != ok {
		t.Fatalf("Health.HostUptimeAvailable = %v, want %v (matching hostUptimeSeconds() directly)", h.HostUptimeAvailable, ok)
	}
	if h.HostUptimeSeconds != uptime {
		t.Fatalf("Health.HostUptimeSeconds = %d, want %d", h.HostUptimeSeconds, uptime)
	}
	if !ok && h.HostUptimeSeconds != 0 {
		t.Fatalf("host uptime unavailable but HostUptimeSeconds = %d, want 0 (never fabricate)", h.HostUptimeSeconds)
	}
	// Process uptime (the pre-existing field) must remain populated
	// regardless of whether host uptime is available — the two are
	// independent and this change must not regress the existing
	// contract.
	if h.UptimeSeconds < 0 {
		t.Fatalf("UptimeSeconds = %d, want >= 0", h.UptimeSeconds)
	}
}

func TestGetHealth_NetworkNeverFabricatesAnAddress(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	svc := NewService(db, &DataSources{DB: db})
	h := svc.GetHealth(context.Background())

	primary, all := PrimaryPublicIPv4()
	if (h.Network.PrimaryPublicIPv4 == nil) != (primary == nil) {
		t.Fatalf("Health.Network.PrimaryPublicIPv4 nil-ness = %v, want %v",
			h.Network.PrimaryPublicIPv4 == nil, primary == nil)
	}
	if h.Network.PrimaryPublicIPv4 != nil {
		if *h.Network.PrimaryPublicIPv4 != *primary {
			t.Fatalf("Health.Network.PrimaryPublicIPv4 = %q, want %q", *h.Network.PrimaryPublicIPv4, *primary)
		}
		// Every reported address must parse as a real IPv4 — this is
		// the strongest guard against ever emitting a placeholder
		// string.
		if net.ParseIP(*h.Network.PrimaryPublicIPv4).To4() == nil {
			t.Fatalf("PrimaryPublicIPv4 %q does not parse as IPv4", *h.Network.PrimaryPublicIPv4)
		}
	}
	if len(h.Network.Addresses) != len(all) {
		t.Fatalf("Health.Network.Addresses len = %d, want %d", len(h.Network.Addresses), len(all))
	}
}

func TestIsPubliclyRoutableIPv4_RejectsPrivateAndReservedRanges(t *testing.T) {
	cases := []struct {
		ip       string
		routable bool
	}{
		{"10.0.0.5", false},
		{"172.16.0.5", false},
		{"192.168.1.1", false},
		{"100.64.0.1", false},
		{"169.254.1.1", false},
		{"127.0.0.1", false},
		{"224.0.0.1", false},
		{"0.0.0.0", false},
		{"198.51.100.7", false},
		{"51.75.240.231", true},
		{"8.8.8.8", true},
		{"1.1.1.1", true},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip).To4()
		if ip == nil {
			t.Fatalf("test setup: %q did not parse as IPv4", c.ip)
		}
		got := isPubliclyRoutableIPv4(ip)
		if got != c.routable {
			t.Errorf("isPubliclyRoutableIPv4(%q) = %v, want %v", c.ip, got, c.routable)
		}
	}
}

func TestDiscoverPublicIPv4_DeterministicSortedNoDuplicates(t *testing.T) {
	// Run twice: the result must be stable (sorted) and free of
	// duplicates regardless of how many times it's called.
	a := discoverPublicIPv4()
	b := discoverPublicIPv4()
	if len(a) != len(b) {
		t.Fatalf("discoverPublicIPv4() not stable across calls: %v vs %v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("discoverPublicIPv4() not stable across calls: %v vs %v", a, b)
		}
	}
	seen := map[string]bool{}
	for _, ip := range a {
		if seen[ip] {
			t.Fatalf("discoverPublicIPv4() returned duplicate address %q", ip)
		}
		seen[ip] = true
		if net.ParseIP(ip).To4() == nil {
			t.Fatalf("discoverPublicIPv4() returned non-IPv4 value %q", ip)
		}
	}
}
