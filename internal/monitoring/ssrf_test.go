package monitoring

import (
	"context"
	"net"
	"strings"
	"testing"
)

type rebindingResolver struct{ calls int }

func (r *rebindingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.calls++
	if r.calls == 1 {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

type countingDialer struct{ calls int }

func (d *countingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls++
	return nil, context.DeadlineExceeded
}

func TestValidateWebhookURL_ValidHTTPS(t *testing.T) {
	if err := ValidateWebhookURL("https://example.com/webhook"); err != nil {
		t.Fatalf("valid HTTPS URL should pass: %v", err)
	}
}

func TestValidateWebhookURL_HTTPRejected(t *testing.T) {
	if err := ValidateWebhookURL("http://hooks.example.com/webhook"); err == nil {
		t.Fatal("HTTP URL must be rejected")
	}
}

func TestValidateWebhookURL_LoopbackIPv4(t *testing.T) {
	if err := ValidateWebhookURL("https://127.0.0.1/hook"); err == nil {
		t.Fatal("loopback IPv4 must be rejected")
	}
}

func TestValidateWebhookURL_LoopbackIPv6(t *testing.T) {
	if err := ValidateWebhookURL("https://[::1]/hook"); err == nil {
		t.Fatal("loopback IPv6 must be rejected")
	}
}

func TestValidateWebhookURL_Private10(t *testing.T) {
	if err := ValidateWebhookURL("https://10.0.0.1/hook"); err == nil {
		t.Fatal("private 10.x must be rejected")
	}
}

func TestValidateWebhookURL_Private172_16(t *testing.T) {
	if err := ValidateWebhookURL("https://172.16.0.1/hook"); err == nil {
		t.Fatal("private 172.16.x must be rejected")
	}
}

func TestValidateWebhookURL_Private192_168(t *testing.T) {
	if err := ValidateWebhookURL("https://192.168.1.1/hook"); err == nil {
		t.Fatal("private 192.168.x must be rejected")
	}
}

func TestValidateWebhookURL_LinkLocal(t *testing.T) {
	if err := ValidateWebhookURL("https://169.254.169.254/hook"); err == nil {
		t.Fatal("link-local must be rejected")
	}
}

func TestValidateWebhookURL_EmptyURL(t *testing.T) {
	if err := ValidateWebhookURL(""); err == nil {
		t.Fatal("empty URL must be rejected")
	}
}

func TestValidateWebhookURL_MalformedURL(t *testing.T) {
	err := ValidateWebhookURL("://bad-url")
	if err == nil {
		t.Fatal("malformed URL must be rejected")
	}
	if !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("expected 'invalid URL' error, got: %v", err)
	}
}

func TestUnsafeSpecialUseAddressesRejected(t *testing.T) {
	for _, address := range []string{
		"100.64.0.1", "192.0.0.1", "198.18.0.1", "240.0.0.1",
		"::ffff:127.0.0.1", "fc00::1", "fe80::1",
	} {
		t.Run(address, func(t *testing.T) {
			if !isUnsafeIP(net.ParseIP(address)) {
				t.Fatalf("%s must be blocked", address)
			}
		})
	}
}

func TestWebhookDialRevalidatesAndBlocksDNSRebinding(t *testing.T) {
	resolver := &rebindingResolver{}
	dialer := &countingDialer{}
	provider, err := newWebhookProvider(
		WebhookConfig{Enabled: true, URL: "https://example.com/hook"},
		resolver,
		dialer,
		nil,
	)
	if err != nil {
		t.Fatalf("construct provider: %v", err)
	}
	if err := provider.Deliver(context.Background(), sampleAlert()); err == nil {
		t.Fatal("rebinding delivery must fail")
	}
	if resolver.calls < 2 {
		t.Fatalf("expected validation and dial-time resolution, got %d calls", resolver.calls)
	}
	if dialer.calls != 0 {
		t.Fatal("unsafe rebound address reached the socket dialer")
	}
}
