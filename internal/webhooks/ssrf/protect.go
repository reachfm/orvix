package ssrf

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Allowlist struct {
	AllowedHosts map[string]bool
	DevMode      bool
}

var ErrProhibitedDestination = fmt.Errorf("webhook ssrf: destination address is prohibited")

func ValidateURL(rawURL string, allow *Allowlist) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("webhook ssrf: invalid URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("webhook ssrf: only https is allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook ssrf: missing host")
	}
	if allow != nil && allow.DevMode && allow.AllowedHosts[host] {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return fmt.Errorf("webhook ssrf: cannot resolve host %s: %w", host, err)
	}
	for _, ipa := range ips {
		if isProhibited(ipa.IP) {
			return fmt.Errorf("%w: %s resolves to %s", ErrProhibitedDestination, host, ipa.IP)
		}
	}
	return nil
}

func isProhibited(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	private := []*net.IPNet{
		{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
		{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
		{IP: net.ParseIP("127.0.0.0"), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("169.254.0.0"), Mask: net.CIDRMask(16, 32)},
		{IP: net.ParseIP("fd00::"), Mask: net.CIDRMask(8, 120)},
	}
	for _, n := range private {
		if n.Contains(ip) {
			return true
		}
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	return false
}

func SafeHTTPClient(timeout time.Duration, allow *Allowlist) *http.Client {
	d := &net.Dialer{Timeout: timeout}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := ValidateURL(req.URL.String(), allow); err != nil {
				return err
			}
			if len(via) >= 5 {
				return fmt.Errorf("webhook ssrf: too many redirects")
			}
			return nil
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				var lastErr error
				for _, ipa := range ips {
					if isProhibited(ipa.IP) {
						lastErr = fmt.Errorf("%w: %s", ErrProhibitedDestination, ipa.IP)
						continue
					}
					address := net.JoinHostPort(ipa.IP.String(), port)
					conn, err := d.DialContext(ctx, network, address)
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				return nil, lastErr
			},
		},
	}
}
