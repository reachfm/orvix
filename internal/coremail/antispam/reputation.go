package antispam

import (
	"net"
	"sync"
)

// MemoryReporter provides reputation lookups from in-memory lists.
// This is the default implementation for tests and small deployments.
type MemoryReporter struct {
	mu       sync.RWMutex
	badIPs   []net.IP
	badCIDRs []*net.IPNet
	allowedIPs   []net.IP
	allowedCIDRs []*net.IPNet
	domains  map[string]DomainReputation
}

func NewMemoryReporter() *MemoryReporter {
	return &MemoryReporter{
		domains: make(map[string]DomainReputation),
	}
}

func (m *MemoryReporter) AddBadIP(ip net.IP) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.badIPs = append(m.badIPs, ip)
}

func (m *MemoryReporter) AddBadCIDR(cidr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, net, err := net.ParseCIDR(cidr)
	if err == nil {
		m.badCIDRs = append(m.badCIDRs, net)
	}
}

func (m *MemoryReporter) AddAllowedIP(ip net.IP) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allowedIPs = append(m.allowedIPs, ip)
}

func (m *MemoryReporter) AddAllowedCIDR(cidr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, net, err := net.ParseCIDR(cidr)
	if err == nil {
		m.allowedCIDRs = append(m.allowedCIDRs, net)
	}
}

func (m *MemoryReporter) SetDomainReputation(domain string, rep DomainReputation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.domains[domain] = rep
}

func (m *MemoryReporter) IsBadIP(ip net.IP) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.badIPs {
		if b.Equal(ip) {
			return true
		}
	}
	for _, c := range m.badCIDRs {
		if c.Contains(ip) {
			return true
		}
	}
	return false
}

func (m *MemoryReporter) IsAllowedIP(ip net.IP) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range m.allowedIPs {
		if a.Equal(ip) {
			return true
		}
	}
	for _, c := range m.allowedCIDRs {
		if c.Contains(ip) {
			return true
		}
	}
	return false
}

func (m *MemoryReporter) SenderDomainReputation(domain string) DomainReputation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rep, ok := m.domains[domain]
	if !ok {
		return DomainReputation{Confidence: 0}
	}
	return rep
}
