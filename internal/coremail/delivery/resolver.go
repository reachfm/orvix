package delivery

import (
	"context"
	"fmt"
	"net"
	"sort"
)

// MXRecord represents a resolved MX record with priority.
type MXRecord struct {
	Host     string
	Priority int
}

// Resolver resolves recipient domains to delivery targets.
type Resolver interface {
	LookupMX(ctx context.Context, domain string) ([]MXRecord, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// DNSResolver performs real DNS lookups for MX and A/AAAA records.
type DNSResolver struct{}

func NewDNSResolver() *DNSResolver { return &DNSResolver{} }

func (r *DNSResolver) LookupMX(ctx context.Context, domain string) ([]MXRecord, error) {
	mxRecords, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("mx lookup %s: %w", domain, err)
	}
	if len(mxRecords) == 0 {
		return nil, fmt.Errorf("no mx records for %s", domain)
	}
	result := make([]MXRecord, len(mxRecords))
	for i, mx := range mxRecords {
		result[i] = MXRecord{Host: mx.Host, Priority: int(mx.Pref)}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Priority < result[j].Priority
	})
	return result, nil
}

func (r *DNSResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("host lookup %s: %w", host, err)
	}
	return addrs, nil
}

// FakeResolver returns predetermined MX records and addresses for testing.
type FakeResolver struct {
	MXRecords  map[string][]MXRecord
	Hosts      map[string][]string
	FailDomain string
	FailHost   string
}

func NewFakeResolver() *FakeResolver {
	return &FakeResolver{
		MXRecords: make(map[string][]MXRecord),
		Hosts:     make(map[string][]string),
	}
}

func (r *FakeResolver) LookupMX(ctx context.Context, domain string) ([]MXRecord, error) {
	if r.FailDomain != "" && domain == r.FailDomain {
		return nil, fmt.Errorf("simulated mx failure for %s", domain)
	}
	if records, ok := r.MXRecords[domain]; ok && len(records) > 0 {
		cp := make([]MXRecord, len(records))
		copy(cp, records)
		sort.Slice(cp, func(i, j int) bool { return cp[i].Priority < cp[j].Priority })
		return cp, nil
	}
	return []MXRecord{{Host: "mail." + domain, Priority: 10}}, nil
}

func (r *FakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if r.FailHost != "" && host == r.FailHost {
		return nil, fmt.Errorf("simulated host lookup failure for %s", host)
	}
	if addrs, ok := r.Hosts[host]; ok && len(addrs) > 0 {
		return addrs, nil
	}
	return []string{"127.0.0.1"}, nil
}
