package delivery

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSMTPDialAddressUsesJoinHostPort(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "ipv4", host: "192.0.2.10", want: "192.0.2.10:25"},
		{name: "ipv6", host: "2607:f8b0:4023:1013::1a", want: "[2607:f8b0:4023:1013::1a]:25"},
		{name: "mx hostname", host: "gmail-smtp-in.l.google.com", want: "gmail-smtp-in.l.google.com:25"},
		{name: "explicit hostname port", host: "mx.example.com:2525", want: "mx.example.com:2525"},
		{name: "explicit bracketed ipv6 port", host: "[2607:f8b0:4023:1013::1a]:2525", want: "[2607:f8b0:4023:1013::1a]:2525"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := smtpDialAddress(tt.host, "25"); got != tt.want {
				t.Fatalf("smtpDialAddress(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestOrderResolvedAddressesPreferIPv4(t *testing.T) {
	addrs := []string{
		"2607:f8b0:4023:1013::1a",
		"142.250.102.27",
		"2607:f8b0:4023:1013::1b",
		"74.125.200.27",
	}
	got := orderResolvedAddresses(addrs, true)
	want := []string{
		"142.250.102.27",
		"74.125.200.27",
		"2607:f8b0:4023:1013::1a",
		"2607:f8b0:4023:1013::1b",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ordered addresses = %v, want %v", got, want)
	}
}

func TestOrderResolvedAddressesPreservesResolverOrderWhenDisabled(t *testing.T) {
	addrs := []string{"2607:f8b0:4023:1013::1a", "142.250.102.27"}
	got := orderResolvedAddresses(addrs, false)
	if strings.Join(got, ",") != strings.Join(addrs, ",") {
		t.Fatalf("ordered addresses = %v, want original %v", got, addrs)
	}
}

func TestTransportConnectFailureIsTemporary(t *testing.T) {
	transport := NewSMTPTransport(TransportConfig{ConnectTimeout: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result := transport.Deliver(ctx, "127.0.0.1:1", false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected connect failure")
	}
	if !result.TempFail {
		t.Fatalf("connect failure must be retryable, got TempFail=false status=%q", result.StatusMsg)
	}
	if !strings.Contains(result.StatusMsg, "connect failed") {
		t.Fatalf("expected connect failure status, got %q", result.StatusMsg)
	}
}

func TestRetryClassificationForNetworkAndSMTPStatus(t *testing.T) {
	policy := FastRetryPolicy()

	decision, _, err := policy.ClassifyResult(&DeliveryResult{StatusMsg: "connect failed: refused", TempFail: true}, 1)
	if err != nil {
		t.Fatalf("classify connect: %v", err)
	}
	if decision != DecisionRetry {
		t.Fatalf("connect failure decision = %v, want retry", decision)
	}

	decision, _, err = policy.ClassifyResult(&DeliveryResult{StatusCode: 451, StatusMsg: "try again later", TempFail: true}, 1)
	if err != nil {
		t.Fatalf("classify 4xx: %v", err)
	}
	if decision != DecisionRetry {
		t.Fatalf("SMTP 4xx decision = %v, want retry", decision)
	}

	decision, _, err = policy.ClassifyResult(&DeliveryResult{StatusCode: 550, StatusMsg: "user unknown", TempFail: false}, 1)
	if err != nil {
		t.Fatalf("classify 5xx: %v", err)
	}
	if decision != DecisionDeadLetter {
		t.Fatalf("SMTP 5xx decision = %v, want dead-letter/bounce", decision)
	}
}
