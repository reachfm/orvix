package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

// smtpTLSRoots allows tests to inject custom trust roots.
// Only set by test code — production code never touches this.
var smtpTLSRoots *x509.CertPool

// smtpForceImplicitTLS allows tests to force implicit TLS mode
// regardless of port. Only set by test code.
var smtpForceImplicitTLS bool

// DialSMTPWithTLS establishes a TLS-secured SMTP connection and authenticates.
// It supports two modes:
//
//	A. Implicit TLS (port 465): connects with TLS first, then SMTP.
//	B. STARTTLS (other ports, typically 587): plaintext connect, upgrade.
//
// Authentication is performed only after TLS is established.
// Connections are closed on any failure.
func DialSMTPWithTLS(host string, port int, username, password string) (*smtp.Client, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	tlsConfig := &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
	}
	if smtpTLSRoots != nil {
		tlsConfig.RootCAs = smtpTLSRoots
	}

	isImplicitTLS := smtpForceImplicitTLS || port == 465

	var client *smtp.Client
	var err error

	if isImplicitTLS {
		conn, dialErr := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, tlsConfig)
		if dialErr != nil {
			return nil, fmt.Errorf("implicit TLS connection to %s: %w", addr, dialErr)
		}
		client, err = smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("SMTP client after implicit TLS: %w", err)
		}
	} else {
		conn, dialErr := net.DialTimeout("tcp", addr, 10*time.Second)
		if dialErr != nil {
			return nil, fmt.Errorf("TCP connection to %s: %w", addr, dialErr)
		}

		client, err = smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("SMTP client: %w", err)
		}

		ok, _ := client.Extension("STARTTLS")
		if !ok {
			client.Close()
			return nil, fmt.Errorf("mandatory TLS required for SMTP authentication: server %s does not advertise STARTTLS", host)
		}

		if startErr := client.StartTLS(tlsConfig); startErr != nil {
			client.Close()
			return nil, fmt.Errorf("STARTTLS failed for %s: %w", host, startErr)
		}
	}

	auth := smtp.PlainAuth("", username, password, host)
	if authErr := client.Auth(auth); authErr != nil {
		client.Close()
		return nil, fmt.Errorf("SMTP authentication failed for %s: %w", host, authErr)
	}

	return client, nil
}
