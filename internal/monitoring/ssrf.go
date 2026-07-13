package monitoring

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
)

type webhookResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type systemWebhookResolver struct{ resolver *net.Resolver }

func (r systemWebhookResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return r.resolver.LookupIPAddr(ctx, host)
}

var blockedWebhookPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func ValidateWebhookURL(rawURL string) error {
	return validateWebhookURL(context.Background(), rawURL, systemWebhookResolver{resolver: net.DefaultResolver})
}

func validateWebhookURL(ctx context.Context, rawURL string, resolver webhookResolver) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use HTTPS")
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("webhook URL has no host")
	}
	if u.User != nil {
		return fmt.Errorf("webhook URL must not contain user information")
	}
	_, err = resolveSafeWebhookIPs(ctx, resolver, u.Hostname())
	return err
}

func resolveSafeWebhookIPs(ctx context.Context, resolver webhookResolver, host string) ([]net.IPAddr, error) {
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve webhook host")
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("webhook host resolved to no addresses")
	}
	for _, address := range addresses {
		if isUnsafeIP(address.IP) {
			return nil, fmt.Errorf("webhook host resolves to an unsafe address")
		}
	}
	return addresses, nil
}

func isUnsafeIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() {
		return true
	}
	for _, prefix := range blockedWebhookPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
