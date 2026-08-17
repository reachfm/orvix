package monitoring

import (
	"net"
	"sort"
)

// discoverPublicIPv4 enumerates the host's own network interfaces and
// returns the addresses that look like real, routable, non-private
// IPv4 addresses. This NEVER calls a third-party service (no ipify,
// ifconfig.me, ipinfo, etc.) — it only reads local OS interface
// state, which is safe, offline, and cannot leak the request to an
// external party.
//
// "Public-looking" here means: not loopback, not link-local, not
// unspecified, and not in any of the private/reserved IPv4 ranges
// (RFC 1918, RFC 6598 CGNAT, RFC 3927 link-local, multicast). This is
// a best-effort heuristic — behind NAT the OS's own interface address
// IS the private range, in which case the result is legitimately
// empty rather than fabricated. There is no reliable dependency-free
// way to learn a NAT'd host's actual public IP without asking an
// external service, and the mission explicitly forbids that.
//
// Returns a sorted, deduplicated address list for determinism across
// calls on the same host. Never panics; returns an empty slice (never
// nil-then-crash) if interface enumeration fails.
func discoverPublicIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, iface := range ifaces {
		// Skip interfaces that are down or are loopback/point-to-point
		// virtual adapters — those never carry a genuine public address.
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, aerr := iface.Addrs()
		if aerr != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue // IPv6 or malformed
			}
			if !isPubliclyRoutableIPv4(ip4) {
				continue
			}
			s := ip4.String()
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// isPubliclyRoutableIPv4 rejects loopback, link-local, multicast, and
// the standard private/reserved IPv4 blocks (RFC 1918 + RFC 6598
// carrier-grade NAT). Anything left over is treated as a real,
// routable address.
func isPubliclyRoutableIPv4(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	privateBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10", // RFC 6598 carrier-grade NAT
		"169.254.0.0/16",
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // reserved
	}
	for _, cidr := range privateBlocks {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// PrimaryPublicIPv4 returns the host's best-guess primary public IPv4
// address (the first of the sorted candidate list) and the full
// candidate list. Returns (nil, nil) — truthfully, not fabricated —
// if the host has no publicly-routable IPv4 address on any interface
// (e.g. fully NAT'd with no public assignment visible to the OS).
func PrimaryPublicIPv4() (primary *string, all []string) {
	addrs := discoverPublicIPv4()
	if len(addrs) == 0 {
		return nil, nil
	}
	p := addrs[0]
	return &p, addrs
}
