package dnsops

import (
	"context"
	"net"
)

// Resolver abstracts DNS lookups for the Verifier and the manual
// provider's live-state read pass. Production uses NetResolver (which
// wraps net.DefaultResolver); tests use FakeResolver (in-memory map).
//
// We intentionally duplicate this small interface in dnsops rather
// than reuse internal/coremail/spf.DNSResolver, because:
//
//   - the spf package's interface couples LookupA to *net.IP and
//     exposes only what SPF needs; dnsops needs PTR lookup as well
//     and a stable, package-local contract that the FakeResolver
//     can satisfy without an spf dependency;
//   - dnsops must remain buildable in isolation from the mail
//     protocol code paths so future tests can stub the DNS layer
//     without spinning up an SMTP/IMAP test rig.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupA(ctx context.Context, name string) ([]net.IP, error)
	LookupAAAA(ctx context.Context, name string) ([]net.IP, error)
	LookupPTR(ctx context.Context, ip string) ([]string, error)
}

// NetResolver wraps net.Resolver to satisfy Resolver. We use the
// process-wide default resolver so the operator's resolv.conf /
// systemd-resolved are honoured. We never shell out to dig/nslookup.
type NetResolver struct {
	resolver *net.Resolver
}

// NewNetResolver returns a Resolver backed by net.DefaultResolver.
func NewNetResolver() *NetResolver {
	return &NetResolver{resolver: net.DefaultResolver}
}

// LookupTXT returns all TXT strings for name.
func (r *NetResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return r.resolver.LookupTXT(ctx, name)
}

// LookupMX returns the MX records for name.
func (r *NetResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return r.resolver.LookupMX(ctx, name)
}

// LookupA returns IPv4 addresses for name.
func (r *NetResolver) LookupA(ctx context.Context, name string) ([]net.IP, error) {
	ips, err := r.resolver.LookupNetIP(ctx, "ip4", name)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if v4 := ip.AsSlice(); len(v4) == 4 || (len(v4) == 16 && isV4Mapped(v4)) {
			out = append(out, net.IP(ip.AsSlice()))
		}
	}
	return out, nil
}

// LookupAAAA returns IPv6 addresses for name.
func (r *NetResolver) LookupAAAA(ctx context.Context, name string) ([]net.IP, error) {
	ips, err := r.resolver.LookupNetIP(ctx, "ip6", name)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IP(ip.AsSlice()))
	}
	return out, nil
}

// LookupPTR performs a reverse DNS lookup. Go's net.Resolver does not
// expose a generic PTR method; we use LookupAddr on the reverse form
// if the input parses as an IP, otherwise we treat the input as a
// name and return what we find (which is normally an error).
func (r *NetResolver) LookupPTR(ctx context.Context, ip string) ([]string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil, &net.DNSError{Err: "invalid IP for reverse lookup", Name: ip}
	}
	return r.resolver.LookupAddr(ctx, parsed.String())
}

func isV4Mapped(b []byte) bool {
	if len(b) != 16 {
		return false
	}
	for i := 0; i < 10; i++ {
		if b[i] != 0 {
			return false
		}
	}
	return b[10] == 0xff && b[11] == 0xff
}

// ── Fake Resolver (tests only) ─────────────────────────────────

// FakeEntry holds canned DNS data for one name. Any zero field is
// treated as "no records of that type" and LookupXxx returns a
// "no such host" DNSError.
type FakeEntry struct {
	TXT    []string
	MX     []net.MX
	A      []net.IP
	AAAA   []net.IP
	PTRFor map[string][]string // IP -> []host (PTR answers)
}

// FakeResolver is an in-memory Resolver used by tests. The DNS Ops
// tests construct one of these per scenario so we never need live
// internet for verification tests.
type FakeResolver struct {
	entries map[string]FakeEntry
}

// NewFakeResolver returns an empty FakeResolver.
func NewFakeResolver() *FakeResolver {
	return &FakeResolver{entries: make(map[string]FakeEntry)}
}

// Set installs the entry for name.
func (f *FakeResolver) Set(name string, e FakeEntry) {
	f.entries[name] = e
}

// LookupTXT implements Resolver.
func (f *FakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	e, ok := f.entries[name]
	if !ok || len(e.TXT) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	out := make([]string, len(e.TXT))
	copy(out, e.TXT)
	return out, nil
}

// LookupMX implements Resolver.
func (f *FakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	e, ok := f.entries[name]
	if !ok || len(e.MX) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	out := make([]*net.MX, len(e.MX))
	for i := range e.MX {
		mc := e.MX[i]
		out[i] = &mc
	}
	return out, nil
}

// LookupA implements Resolver.
func (f *FakeResolver) LookupA(_ context.Context, name string) ([]net.IP, error) {
	e, ok := f.entries[name]
	if !ok || len(e.A) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	out := make([]net.IP, len(e.A))
	copy(out, e.A)
	return out, nil
}

// LookupAAAA implements Resolver.
func (f *FakeResolver) LookupAAAA(_ context.Context, name string) ([]net.IP, error) {
	e, ok := f.entries[name]
	if !ok || len(e.AAAA) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	out := make([]net.IP, len(e.AAAA))
	copy(out, e.AAAA)
	return out, nil
}

// LookupPTR implements Resolver. We look up by exact IP string match.
func (f *FakeResolver) LookupPTR(_ context.Context, ip string) ([]string, error) {
	for _, e := range f.entries {
		if vs, ok := e.PTRFor[ip]; ok && len(vs) > 0 {
			out := make([]string, len(vs))
			copy(out, vs)
			return out, nil
		}
	}
	return nil, &net.DNSError{Err: "no such host", Name: ip, IsNotFound: true}
}
