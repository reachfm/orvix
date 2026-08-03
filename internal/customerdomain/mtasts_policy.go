package customerdomain

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type MTASTSPolicy struct {
	Raw    string
	Valid  bool
	Mode   string
	MaxAge int
	MX     []string
	Error  string
}

type MTASTSPolicyCheck struct {
	*MTASTSCheck
	Policy *MTASTSPolicy `json:"policy,omitempty"`
}

var ssrfBlockedCIDRs = [...]string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"100.64.0.0/10",
	"198.18.0.0/15",
	"224.0.0.0/4",
	"ff00::/8",
	"0.0.0.0/8",
}

func isPublicAddress(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range ssrfBlockedCIDRs {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil && block.Contains(ip) {
			return false
		}
	}
	return true
}

func ssrfDialer(resolver func(ctx context.Context, host string) ([]net.IPAddr, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("mtasts: bad address: %w", err)
		}
		if port != "443" {
			return nil, fmt.Errorf("mtasts: non-tls port rejected")
		}
		ips, err := resolver(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("mtasts: resolve error: %w", err)
		}
		for _, ipAddr := range ips {
			if !isPublicAddress(ipAddr.IP) {
				return nil, fmt.Errorf("mtasts: address %s rejected", ipAddr.IP)
			}
		}
		d := &net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
}

type MTASTSFetcher struct {
	client  *http.Client
	timeout time.Duration
	maxSize int64
}

func NewMTASTSFetcher(resolver func(ctx context.Context, host string) ([]net.IPAddr, error)) *MTASTSFetcher {
	transport := &http.Transport{
		DialContext:       ssrfDialer(resolver),
		ForceAttemptHTTP2: false,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
	}
	f := &MTASTSFetcher{
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Host != via[0].URL.Host || req.URL.Path != via[0].URL.Path {
					return http.ErrUseLastResponse
				}
				if len(via) >= 2 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		timeout: 10 * time.Second,
		maxSize: 1024 * 100,
	}
	return f
}

func (f *MTASTSFetcher) Fetch(ctx context.Context, domain string) (*MTASTSPolicy, error) {
	url := "https://mta-sts." + domain + "/.well-known/mta-sts.txt"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("mta-sts request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mta-sts fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mta-sts: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxSize))
	if err != nil {
		return nil, fmt.Errorf("mta-sts read: %w", err)
	}
	return parseMTASTSPolicy(string(body)), nil
}

func parseMTASTSPolicy(raw string) *MTASTSPolicy {
	policy := &MTASTSPolicy{Raw: raw}
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(parts[0]))
		val := strings.TrimSpace(parts[1])
		switch key {
		case "version":
			if val != "STSv1" {
				policy.Error = "invalid version, expected STSv1"
				return policy
			}
		case "mode":
			policy.Mode = val
		case "max_age":
			fmt.Sscanf(val, "%d", &policy.MaxAge)
		case "mx":
			policy.MX = append(policy.MX, val)
		}
	}
	if policy.Mode == "" || policy.MaxAge <= 0 {
		policy.Error = "missing mode or max_age"
		return policy
	}
	if policy.Mode == "enforce" || policy.Mode == "testing" {
		if len(policy.MX) == 0 {
			policy.Error = "mx required for enforce/testing mode"
			return policy
		}
	}
	policy.Valid = true
	return policy
}
