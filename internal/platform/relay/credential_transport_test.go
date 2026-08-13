package relay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fix D coverage: a stored relay credential may only ever be transmitted over
// a VERIFIED TLS session.
//
// These tests use a real TLS listener with a real (test-only) CA, so the
// positive case genuinely completes certificate + hostname verification —
// InsecureSkipVerify is never used to make a test pass. The negative cases
// assert on the BYTES THE SERVER RECEIVED, which is the only assertion that
// actually proves the secret did not leave the process.

const testRelayPassword = "s3cr3t-relay-password"

// wireLog records every line a fixture server received, so a test can prove a
// credential was never transmitted.
type wireLog struct {
	mu    sync.Mutex
	lines []string
}

func (w *wireLog) add(s string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, s)
}

func (w *wireLog) all() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.lines, "\n")
}

// containsSecret reports whether the recorded traffic carries the credential
// in any form AUTH PLAIN could put on the wire: literal, or base64 inside the
// RFC 4616 "\0user\0pass" payload.
func (w *wireLog) containsSecret(t *testing.T, secret string) bool {
	t.Helper()
	all := w.all()
	if strings.Contains(all, secret) {
		return true
	}
	// The AUTH PLAIN payload is base64; decode every AUTH argument we saw.
	for _, line := range strings.Split(all, "\n") {
		up := strings.ToUpper(strings.TrimSpace(line))
		if !strings.HasPrefix(up, "AUTH ") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		for _, f := range fields[1:] {
			if dec, err := base64Decode(f); err == nil && bytes.Contains(dec, []byte(secret)) {
				return true
			}
		}
	}
	return false
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// testCA issues a certificate for 127.0.0.1 and returns the pool that trusts
// it, so strict verification succeeds against the local fixture.
func testCA(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "orvix-relay-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// tlsSMTPServer is an implicit-TLS SMTP fixture that speaks enough of the
// protocol for connect + EHLO + AUTH + MAIL/RCPT/DATA + QUIT.
func tlsSMTPServer(t *testing.T, log *wireLog) (host string, port int, roots *x509.CertPool) {
	t.Helper()
	cert, pool := testCA(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go serveSMTP(ln, log)

	h, p := splitHostPort(t, ln.Addr().String())
	return h, p, pool
}

// plainSMTPServer is the same fixture without TLS, used to prove the
// credential is never sent over an unencrypted session.
func plainSMTPServer(t *testing.T, log *wireLog) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go serveSMTP(ln, log)
	return splitHostPort(t, ln.Addr().String())
}

func serveSMTP(ln net.Listener, log *wireLog) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			w := func(s string) { c.Write([]byte(s + "\r\n")) }
			r := bufio.NewReader(c)
			w("220 orvix-relay-test ESMTP")
			inData := false
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				if log != nil {
					log.add(line)
				}
				if inData {
					if line == "." {
						inData = false
						w("250 2.0.0 accepted")
					}
					continue
				}
				switch {
				case strings.HasPrefix(line, "EHLO"):
					w("250-orvix-relay-test")
					w("250 AUTH PLAIN LOGIN")
				case strings.HasPrefix(line, "AUTH"):
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
		}(conn)
	}
}

// TestAuthOverVerifiedTLS_Succeeds is the positive control: with strict
// validation against a genuinely trusted certificate, AUTH proceeds.
func TestAuthOverVerifiedTLS_Succeeds(t *testing.T) {
	log := &wireLog{}
	host, port, roots := tlsSMTPServer(t, log)
	p := Provider{
		Host: host, Port: port, Username: "relay-user",
		ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationStrict,
	}
	result := dialAndTest(context.Background(), netDialer{}, p, testRelayPassword, roots)
	if !result.Connected {
		t.Fatalf("expected connected, got %q", result.Error)
	}
	if !result.TLSNegotiated {
		t.Fatal("expected a negotiated TLS session")
	}
	if !result.AuthOK {
		t.Fatalf("expected AUTH over verified TLS to succeed, got %q", result.Error)
	}
}

// TestAuthOverPlaintext_RefusedAndCredentialNeverSent is the core negative
// control for the cleartext case.
func TestAuthOverPlaintext_RefusedAndCredentialNeverSent(t *testing.T) {
	log := &wireLog{}
	host, port := plainSMTPServer(t, log)
	p := Provider{
		Host: host, Port: port, Username: "relay-user",
		ConnSecurity: ConnSecurityNone, TLSValidation: TLSValidationStrict,
	}
	result := dialAndTest(context.Background(), netDialer{}, p, testRelayPassword, nil)
	if result.AuthOK {
		t.Fatal("AUTH must not succeed over a plaintext session")
	}
	if log.containsSecret(t, testRelayPassword) {
		t.Fatalf("credential was transmitted over a plaintext session; wire log:\n%s", log.all())
	}
	if strings.Contains(strings.ToUpper(log.all()), "AUTH PLAIN") {
		t.Fatalf("AUTH command was sent over a plaintext session; wire log:\n%s", log.all())
	}
	// The failure must also never echo the secret back to the caller.
	if strings.Contains(result.Error, testRelayPassword) {
		t.Fatal("health-check error leaked the credential")
	}
}

// TestAuthOverOpportunisticTLS_Refused proves that an encrypted-but-unverified
// session is treated as insecure: InsecureSkipVerify means an on-path attacker
// can present any certificate and collect the credential.
func TestAuthOverOpportunisticTLS_Refused(t *testing.T) {
	log := &wireLog{}
	host, port, roots := tlsSMTPServer(t, log)
	p := Provider{
		Host: host, Port: port, Username: "relay-user",
		ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationOpportunistic,
	}
	result := dialAndTest(context.Background(), netDialer{}, p, testRelayPassword, roots)
	if result.AuthOK {
		t.Fatal("AUTH must not succeed over an unverified TLS session")
	}
	if log.containsSecret(t, testRelayPassword) {
		t.Fatalf("credential was transmitted over an unverified TLS session; wire log:\n%s", log.all())
	}
}

// TestStartTLSDowngrade_RefusesAuth proves the connection-time backstop is
// keyed off the ACTUAL session, not the configuration: a provider configured
// for STARTTLS whose server never upgrades must not authenticate. (Here the
// STARTTLS command itself fails against a server that does not offer it, which
// is the desired outcome — the credential never reaches the wire either way.)
func TestStartTLSDowngrade_RefusesAuth(t *testing.T) {
	log := &wireLog{}
	host, port := plainSMTPServer(t, log)
	p := Provider{
		Host: host, Port: port, Username: "relay-user",
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict,
	}
	result := dialAndTest(context.Background(), netDialer{}, p, testRelayPassword, nil)
	if result.AuthOK {
		t.Fatal("AUTH must not succeed when the session was never upgraded to TLS")
	}
	if log.containsSecret(t, testRelayPassword) {
		t.Fatalf("credential leaked on a failed STARTTLS upgrade; wire log:\n%s", log.all())
	}
}

// TestDeliverAuthenticated_RequiresVerifiedTLS proves the REAL delivery path
// (not just the health check) enforces the same policy, and that a compliant
// provider still delivers mail end to end.
func TestDeliverAuthenticated_RequiresVerifiedTLS(t *testing.T) {
	t.Run("verified tls delivers", func(t *testing.T) {
		log := &wireLog{}
		host, port, roots := tlsSMTPServer(t, log)
		p := Provider{
			Host: host, Port: port, Username: "u",
			ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationStrict,
		}
		res := deliverWith(context.Background(), netDialer{}, p, testRelayPassword,
			"a@b.example", []string{"c@d.example"}, []byte("Subject: ok\r\n\r\nbody"), roots)
		if !res.Success {
			t.Fatalf("authenticated delivery over verified TLS must succeed, got %+v", res)
		}
	})

	t.Run("plaintext refuses and defers", func(t *testing.T) {
		log := &wireLog{}
		host, port := plainSMTPServer(t, log)
		p := Provider{
			Host: host, Port: port, Username: "u",
			ConnSecurity: ConnSecurityNone, TLSValidation: TLSValidationStrict,
		}
		res := deliverWith(context.Background(), netDialer{}, p, testRelayPassword,
			"a@b.example", []string{"c@d.example"}, []byte("Subject: no\r\n\r\nbody"), nil)
		if res.Success {
			t.Fatal("authenticated delivery over plaintext must not succeed")
		}
		if !res.TempFail {
			t.Fatal("a refused credential transport must defer, not bounce the message")
		}
		if log.containsSecret(t, testRelayPassword) {
			t.Fatalf("credential leaked on the delivery path; wire log:\n%s", log.all())
		}
		if strings.Contains(res.StatusMsg, testRelayPassword) {
			t.Fatal("delivery status message leaked the credential")
		}
	})
}

// TestValidateCredentialTransport_Matrix pins the configuration-time policy.
func TestValidateCredentialTransport_Matrix(t *testing.T) {
	cases := []struct {
		name      string
		provider  Provider
		wantAllow bool
	}{
		{"no credential, plaintext is allowed", Provider{ConnSecurity: ConnSecurityNone}, true},
		{"no credential, opportunistic is allowed", Provider{ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationOpportunistic}, true},
		{"credential over plaintext refused", Provider{SecretRef: "enc", ConnSecurity: ConnSecurityNone, TLSValidation: TLSValidationStrict}, false},
		{"credential over opportunistic starttls refused", Provider{SecretRef: "enc", ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationOpportunistic}, false},
		{"credential over opportunistic implicit tls refused", Provider{SecretRef: "enc", ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationOpportunistic}, false},
		{"credential over strict starttls allowed", Provider{SecretRef: "enc", ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict}, true},
		{"credential over strict implicit tls allowed", Provider{SecretRef: "enc", ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationStrict}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCredentialTransport(tc.provider)
			if tc.wantAllow && err != nil {
				t.Fatalf("expected allowed, got %v", err)
			}
			if !tc.wantAllow {
				if err == nil {
					t.Fatal("expected the configuration to be refused")
				}
				if !errors.Is(err, ErrInsecureCredentialTransport) {
					t.Fatalf("expected ErrInsecureCredentialTransport, got %v", err)
				}
			}
		})
	}
}

// TestAuthOverUntrustedCertificate_Refused proves strict validation is real:
// the fixture's certificate is NOT in the trust pool passed to the client, so
// the handshake must fail and the credential must never be sent.
func TestAuthOverUntrustedCertificate_Refused(t *testing.T) {
	log := &wireLog{}
	host, port, _ := tlsSMTPServer(t, log) // deliberately discard the pool
	p := Provider{
		Host: host, Port: port, Username: "relay-user",
		ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationStrict,
	}
	// nil roots => the host trust store, which does not know this test CA.
	result := dialAndTest(context.Background(), netDialer{}, p, testRelayPassword, nil)
	if result.AuthOK {
		t.Fatal("AUTH must not succeed against an untrusted certificate")
	}
	if result.TLSNegotiated {
		t.Fatal("an untrusted certificate must not produce a negotiated session")
	}
	if log.containsSecret(t, testRelayPassword) {
		t.Fatalf("credential leaked to a server with an untrusted certificate; wire log:\n%s", log.all())
	}
}

// TestAuthOverWrongHostname_Refused proves hostname verification is enforced,
// not just chain validation. The fixture certificate is issued for 127.0.0.1
// only; connecting under a different name must fail even though the CA is
// trusted.
func TestAuthOverWrongHostname_Refused(t *testing.T) {
	log := &wireLog{}
	_, port, roots := tlsSMTPServer(t, log)
	p := Provider{
		// "localhost" resolves to the same listener but is NOT a SAN on the
		// certificate, which lists only the 127.0.0.1 IP.
		Host: "localhost", Port: port, Username: "relay-user",
		ConnSecurity: ConnSecurityImplicitTLS, TLSValidation: TLSValidationStrict,
	}
	result := dialAndTest(context.Background(), netDialer{}, p, testRelayPassword, roots)
	if result.AuthOK {
		t.Fatal("AUTH must not succeed when the certificate does not cover the hostname")
	}
	if log.containsSecret(t, testRelayPassword) {
		t.Fatalf("credential leaked on a hostname mismatch; wire log:\n%s", log.all())
	}
}

// TestAuthOverStrictStartTLS_Succeeds is the second positive control: a
// STARTTLS upgrade to a verified session may carry the credential.
func TestAuthOverStrictStartTLS_Succeeds(t *testing.T) {
	log := &wireLog{}
	host, port, roots := startTLSSMTPServer(t, log)
	p := Provider{
		Host: host, Port: port, Username: "relay-user",
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict,
	}
	result := dialAndTest(context.Background(), netDialer{}, p, testRelayPassword, roots)
	if !result.Connected {
		t.Fatalf("expected connected, got %q", result.Error)
	}
	if !result.TLSNegotiated {
		t.Fatalf("expected a STARTTLS upgrade, got %q", result.Error)
	}
	if !result.AuthOK {
		t.Fatalf("AUTH over a verified STARTTLS session must succeed, got %q", result.Error)
	}
}

// startTLSSMTPServer speaks plaintext until STARTTLS, then upgrades the same
// connection — the real STARTTLS flow, so the upgrade path is exercised end to
// end rather than simulated.
func startTLSSMTPServer(t *testing.T, log *wireLog) (host string, port int, roots *x509.CertPool) {
	t.Helper()
	cert, pool := testCA(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				w := func(s string) { c.Write([]byte(s + "\r\n")) }
				r := bufio.NewReader(c)
				w("220 orvix-relay-test ESMTP")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					if log != nil {
						log.add(line)
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						w("250-orvix-relay-test")
						w("250-STARTTLS")
						w("250 AUTH PLAIN LOGIN")
					case strings.HasPrefix(line, "STARTTLS"):
						w("220 2.0.0 Ready to start TLS")
						tc := tls.Server(c, &tls.Config{
							Certificates: []tls.Certificate{cert},
							MinVersion:   tls.VersionTLS12,
						})
						if err := tc.Handshake(); err != nil {
							return
						}
						c = tc
						r = bufio.NewReader(c)
						w = func(s string) { tc.Write([]byte(s + "\r\n")) }
					case strings.HasPrefix(line, "AUTH"):
						w("235 2.7.0 Authentication successful")
					case strings.HasPrefix(line, "QUIT"):
						w("221 Bye")
						return
					default:
						w("250 OK")
					}
				}
			}(conn)
		}
	}()

	h, p := splitHostPort(t, ln.Addr().String())
	return h, p, pool
}
