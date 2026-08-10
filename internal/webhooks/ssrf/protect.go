package ssrf

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type Allowlist struct {
	AllowedHosts map[string]bool
	DevMode      bool
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type systemResolver struct{ resolver *net.Resolver }

func (r systemResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return r.resolver.LookupIPAddr(ctx, host)
}

type ClientOptions struct {
	Timeout      time.Duration
	Allowlist    *Allowlist
	Resolver     Resolver
	TLSConfig    *tls.Config
	MaxRedirects int
}

var ErrProhibitedDestination = fmt.Errorf("webhook ssrf: destination address is prohibited")

func allowedForDevelopment(host string, allow *Allowlist) bool {
	return allow != nil && allow.DevMode && allow.AllowedHosts[strings.ToLower(host)]
}

func ValidateURL(rawURL string, allow *Allowlist) error {
	return ValidateURLContext(context.Background(), rawURL, allow, systemResolver{net.DefaultResolver})
}

func ValidateURLContext(ctx context.Context, rawURL string, allow *Allowlist, resolver Resolver) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("webhook ssrf: invalid URL")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("webhook ssrf: only https is allowed")
	}
	if u.User != nil {
		return fmt.Errorf("webhook ssrf: embedded credentials are prohibited")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("webhook ssrf: missing host")
	}
	if allowedForDevelopment(host, allow) {
		return nil
	}
	if resolver == nil {
		resolver = systemResolver{net.DefaultResolver}
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("webhook ssrf: cannot resolve destination")
	}
	for _, candidate := range ips {
		if isProhibited(candidate.IP) {
			return ErrProhibitedDestination
		}
	}
	return nil
}

func isProhibited(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	metadata := []netip.Prefix{
		netip.MustParsePrefix("169.254.169.254/32"),
		netip.MustParsePrefix("100.100.100.200/32"),
		netip.MustParsePrefix("fd00:ec2::254/128"),
	}
	for _, prefix := range metadata {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func SafeHTTPClient(timeout time.Duration, allow *Allowlist) *http.Client {
	return SafeHTTPClientWithOptions(ClientOptions{Timeout: timeout, Allowlist: allow})
}

func SafeHTTPClientWithOptions(options ClientOptions) *http.Client {
	if options.Timeout <= 0 {
		options.Timeout = 15 * time.Second
	}
	if options.MaxRedirects <= 0 {
		options.MaxRedirects = 3
	}
	resolver := options.Resolver
	if resolver == nil {
		resolver = systemResolver{net.DefaultResolver}
	}
	dialer := &net.Dialer{Timeout: min(options.Timeout, 5*time.Second), KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       options.TLSConfig,
		TLSHandshakeTimeout:   min(options.Timeout, 5*time.Second),
		ResponseHeaderTimeout: min(options.Timeout, 10*time.Second),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("webhook connection failed")
			}
			ips, err := resolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("webhook connection failed")
			}
			var lastErr error
			for _, candidate := range ips {
				if !allowedForDevelopment(host, options.Allowlist) && isProhibited(candidate.IP) {
					lastErr = ErrProhibitedDestination
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("webhook connection failed")
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Timeout:   options.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= options.MaxRedirects {
				return fmt.Errorf("webhook ssrf: too many redirects")
			}
			if err := ValidateURLContext(req.Context(), req.URL.String(), options.Allowlist, resolver); err != nil {
				return fmt.Errorf("unsafe webhook redirect")
			}
			return nil
		},
	}
}
