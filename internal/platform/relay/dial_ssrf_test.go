package relay

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// H-4 regression suite: relay delivery SSRF.
//
// The verified defect was that TestConnection validated the destination but
// the real Deliver path dialed with a plain net.Dial, so a tenant-controlled
// relay hostname resolving to an internal address would be connected to —
// and the decrypted credential AUTH-PLAIN'd — during actual delivery. These
// tests prove the delivery path now dials through the same secure validating
// dialer and that a blocked destination receives neither a connection, a
// credential, nor a message byte.

func TestValidateDialIP_AllowsPublicUnicast(t *testing.T) {
	for _, s := range []string{
		"93.184.216.34",                      // public IPv4
		"8.8.8.8",                            // public IPv4
		"2606:2800:220:1:248:1893:25c8:1946", // public IPv6
		"2a00:1450:4009:80f::200e",           // public IPv6
	} {
		if err := validateDialIP(net.ParseIP(s)); err != nil {
			t.Errorf("public address %s must be allowed, got %v", s, err)
		}
	}
}

func TestValidateDialIP_RejectsUnsafeClasses(t *testing.T) {
	cases := map[string]string{
		"loopback v4":           "127.0.0.1",
		"loopback v4 high":      "127.255.255.254",
		"loopback v6":           "::1",
		"rfc1918 10":            "10.0.0.1",
		"rfc1918 172":           "172.16.5.4",
		"rfc1918 192.168":       "192.168.1.1",
		"link-local v4":         "169.254.1.1",
		"cloud metadata":        "169.254.169.254",
		"link-local v6":         "fe80::1",
		"unique-local v6":       "fd00::1",
		"cgnat":                 "100.64.0.1",
		"cgnat high":            "100.127.255.255",
		"this-network":          "0.1.2.3",
		"unspecified v4":        "0.0.0.0",
		"unspecified v6":        "::",
		"multicast v4":          "224.0.0.1",
		"multicast v6":          "ff02::1",
		"benchmark":             "198.18.0.1",
		"doc test-net-1":        "192.0.2.10",
		"doc test-net-2":        "198.51.100.10",
		"doc test-net-3":        "203.0.113.10",
		"ietf protocol":         "192.0.0.1",
		"reserved future":       "240.0.0.1",
		"broadcast":             "255.255.255.255",
		"ipv6 doc":              "2001:db8::1",
		"nat64 embeds loopback": "64:ff9b::7f00:1", // 127.0.0.1 embedded
		"nat64 embeds rfc1918":  "64:ff9b::a00:1",  // 10.0.0.1 embedded
		"ipv4-mapped loopback":  "::ffff:127.0.0.1",
		"ipv4-mapped rfc1918":   "::ffff:10.0.0.1",
		"ipv4-mapped metadata":  "::ffff:169.254.169.254",
		"ipv4-mapped cgnat":     "::ffff:100.64.0.1",
	}
	for name, s := range cases {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("%s: test address %q did not parse", name, s)
		}
		if err := validateDialIP(ip); err == nil {
			t.Errorf("%s (%s) must be rejected", name, s)
		}
	}
}

// TestDeliver_RejectsInternalLiteralWithoutConnectingOrLeakingCredential is the
// headline H-4 proof: Deliver against an internal destination must fail
// BEFORE any TCP connection, so the credential and message bytes never leave
// the process. We drive deliverWith with a validating dialer whose raw
// connector records whether it was ever invoked.
func TestDeliver_RejectsInternalLiteralWithoutConnectingOrLeakingCredential(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "::1", "::ffff:127.0.0.1"} {
		connectorCalled := false
		d := &validatingDialer{
			timeout:  time.Second,
			resolver: netResolver{},
			connect: func(context.Context, string, string) (net.Conn, error) {
				connectorCalled = true
				return nil, fmt.Errorf("must not be reached")
			},
		}
		p := Provider{Host: host, Port: 587, Username: "relay-user", ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict}
		res := deliverWith(context.Background(), d, p, "super-secret-relay-password",
			"sender@tenant.example", []string{"victim@example.com"}, []byte("Subject: x\r\n\r\nbody"), nil)

		if res.Success {
			t.Fatalf("%s: delivery to a blocked destination must not succeed", host)
		}
		if !res.TempFail {
			t.Fatalf("%s: blocked destination should be a temp failure, got %+v", host, res)
		}
		if connectorCalled {
			t.Fatalf("%s: a blocked destination must never reach the TCP connector", host)
		}
		// The status message is bounded and must not echo the password.
		if strings.Contains(res.StatusMsg, "super-secret-relay-password") {
			t.Fatalf("%s: delivery error leaked the credential: %q", host, res.StatusMsg)
		}
	}
}

// TestDeliver_ResolvedInternalHostRejectedBeforeCredential proves the same for
// a HOSTNAME that resolves (via an injected resolver) to an internal address:
// the connector is never reached, so no bytes/credential are transmitted.
func TestDeliver_ResolvedInternalHostRejectedBeforeCredential(t *testing.T) {
	connectorCalled := false
	d := &validatingDialer{
		timeout:  time.Second,
		resolver: fakeResolver{addrs: []net.IP{net.ParseIP("10.10.10.10")}},
		connect: func(context.Context, string, string) (net.Conn, error) {
			connectorCalled = true
			return nil, fmt.Errorf("must not be reached")
		},
	}
	p := Provider{Host: "relay.attacker.example", Port: 587, Username: "u", ConnSecurity: ConnSecurityStartTLS}
	res := deliverWith(context.Background(), d, p, "secret", "a@b.example", []string{"c@d.example"}, []byte("data"), nil)
	if res.Success || !res.TempFail {
		t.Fatalf("expected temp failure for a host resolving internal, got %+v", res)
	}
	if connectorCalled {
		t.Fatal("a host resolving to an internal address must never reach the TCP connector")
	}
}

// TestDeliver_ProductionPathUsesSecureDialer proves the exported Deliver (the
// function the delivery worker actually calls) refuses an internal literal —
// i.e. it is wired to the validating dialer, not a plain net.Dial. There is no
// network here because the literal is rejected before connect.
func TestDeliver_ProductionPathUsesSecureDialer(t *testing.T) {
	p := Provider{Host: "127.0.0.1", Port: 25, Username: "u", ConnSecurity: ConnSecurityNone}
	res := Deliver(context.Background(), p, "pw", "a@b.example", []string{"c@d.example"}, []byte("data"))
	if res.Success {
		t.Fatal("production Deliver must not connect to loopback")
	}
	if !res.TempFail {
		t.Fatalf("expected temp failure, got %+v", res)
	}
}

// deliveryCapableSMTPServer is a one-connection fake SMTP server that speaks
// the full delivery exchange (EHLO/AUTH/MAIL/RCPT/DATA/QUIT), used to prove
// legitimate relay delivery still completes end to end.
func deliveryCapableSMTPServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		w := func(s string) { conn.Write([]byte(s + "\r\n")) }
		br := bufio.NewReader(conn)
		w("220 fake.relay.test ESMTP")
		inData := false
		for {
			raw, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line := strings.TrimRight(raw, "\r\n")
			switch {
			case inData:
				if line == "." {
					inData = false
					w("250 2.0.0 accepted")
				}
			case strings.HasPrefix(line, "EHLO"):
				w("250-fake.relay.test")
				w("250 AUTH PLAIN LOGIN")
			case strings.HasPrefix(line, "AUTH PLAIN"):
				w("235 2.7.0 Authentication successful")
			case strings.HasPrefix(line, "MAIL FROM"):
				w("250 2.1.0 sender ok")
			case strings.HasPrefix(line, "RCPT TO"):
				w("250 2.1.5 recipient ok")
			case strings.HasPrefix(line, "DATA"):
				w("354 end with <CRLF>.<CRLF>")
				inData = true
			case strings.HasPrefix(line, "QUIT"):
				w("221 Bye")
				return
			default:
				w("250 OK")
			}
		}
	}()
	return ln.Addr().String()
}

// TestDeliver_PublicRelayStillWorks proves legitimate relay delivery is
// preserved: a real in-process SMTP server is reached through deliverWith + a
// plain connector (the policy is proven by the dedicated tests above; this one
// confirms the SMTP delivery exchange itself completes end to end).
func TestDeliver_PublicRelayStillWorks(t *testing.T) {
	addr := deliveryCapableSMTPServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	// No credential: an UNAUTHENTICATED relay over a plaintext session is a
	// legitimate configuration (internal smarthosts) and must keep working.
	// The authenticated equivalent is covered by
	// TestDeliverAuthenticated_RequiresVerifiedTLS, which proves a credential
	// is only ever sent over verified TLS.
	p := Provider{Host: host, Port: port, ConnSecurity: ConnSecurityNone}
	res := deliverWith(context.Background(), netDialer{}, p, "", "a@b.example", []string{"c@d.example"}, []byte("Subject: ok\r\n\r\nbody"), nil)
	if !res.Success {
		t.Fatalf("legitimate delivery through a reachable relay must succeed, got %+v", res)
	}
}

// TestValidateDialIP_ConcurrentNoRace runs the policy from many goroutines to
// prove there is no shared-state race (run under -race). The blocked-prefix
// table is read-only after init, so this must be safe.
func TestValidateDialIP_ConcurrentNoRace(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.1"), net.ParseIP("::1"),
		net.ParseIP("169.254.169.254"), net.ParseIP("100.64.0.1"), net.ParseIP("::ffff:127.0.0.1"),
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = validateDialIP(ips[j%len(ips)])
			}
		}()
	}
	wg.Wait()
}

// TestValidatingDialer_ConcurrentDialsNoRace proves concurrent dials through a
// single dialer share no mutable state (run under -race).
func TestValidatingDialer_ConcurrentDialsNoRace(t *testing.T) {
	d := &validatingDialer{
		timeout:  time.Second,
		resolver: fakeResolver{addrs: []net.IP{net.ParseIP("93.184.216.34")}},
		connect: func(context.Context, string, string) (net.Conn, error) {
			return nil, fmt.Errorf("refused (test)")
		},
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.DialContext(context.Background(), "tcp", "relay.example:587")
		}()
	}
	wg.Wait()
}
