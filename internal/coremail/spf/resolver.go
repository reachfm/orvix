package spf

import (
	"context"
	"net"
)

// DNSResolver abstracts DNS lookups for SPF evaluation.
// Production uses net.Resolver; tests use a fake resolver.
type DNSResolver interface {
	LookupTXT(ctx context.Context, domain string) ([]string, error)
	LookupA(ctx context.Context, domain string) ([]net.IP, error)
	LookupAAAA(ctx context.Context, domain string) ([]net.IP, error)
	LookupMX(ctx context.Context, domain string) ([]*net.MX, error)
}

// NetResolver wraps net.Resolver to implement DNSResolver.
type NetResolver struct {
	resolver *net.Resolver
}

func NewNetResolver() *NetResolver {
	return &NetResolver{resolver: net.DefaultResolver}
}

func (r *NetResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return r.resolver.LookupTXT(ctx, domain)
}

func (r *NetResolver) LookupA(ctx context.Context, domain string) ([]net.IP, error) {
	ips, err := r.resolver.LookupNetIP(ctx, "ip4", domain)
	if err != nil {
		return nil, err
	}
	result := make([]net.IP, len(ips))
	for i, ip := range ips {
		result[i] = net.IP(ip.AsSlice())
	}
	return result, nil
}

func (r *NetResolver) LookupAAAA(ctx context.Context, domain string) ([]net.IP, error) {
	ips, err := r.resolver.LookupNetIP(ctx, "ip6", domain)
	if err != nil {
		return nil, err
	}
	result := make([]net.IP, len(ips))
	for i, ip := range ips {
		result[i] = net.IP(ip.AsSlice())
	}
	return result, nil
}

func (r *NetResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	return r.resolver.LookupMX(ctx, domain)
}

// ── Fake Resolver for Tests ──────────────────────────────────

// FakeResolverEntry holds fake DNS data for a domain.
type FakeResolverEntry struct {
	TXT  []string
	A    []string // IP strings
	AAAA []string // IP strings
	MX   []string // domain names
}

// FakeResolver implements DNSResolver with in-memory data.
type FakeResolver struct {
	entries map[string]FakeResolverEntry
}

func NewFakeResolver() *FakeResolver {
	return &FakeResolver{entries: make(map[string]FakeResolverEntry)}
}

func (f *FakeResolver) Add(domain string, entry FakeResolverEntry) {
	f.entries[domain] = entry
}

func (f *FakeResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	e, ok := f.entries[domain]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
	}
	return e.TXT, nil
}

func (f *FakeResolver) LookupA(ctx context.Context, domain string) ([]net.IP, error) {
	e, ok := f.entries[domain]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
	}
	var ips []net.IP
	for _, s := range e.A {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			ips = append(ips, ip4)
		}
	}
	return ips, nil
}

func (f *FakeResolver) LookupAAAA(ctx context.Context, domain string) ([]net.IP, error) {
	e, ok := f.entries[domain]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
	}
	var ips []net.IP
	for _, s := range e.AAAA {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		if ip.To4() == nil {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

func (f *FakeResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	e, ok := f.entries[domain]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: domain, IsNotFound: true}
	}
	var mx []*net.MX
	for _, s := range e.MX {
		mx = append(mx, &net.MX{Host: s, Pref: 10})
	}
	return mx, nil
}
