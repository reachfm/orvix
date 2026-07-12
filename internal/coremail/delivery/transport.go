package delivery

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"time"
)

// TLSPolicy defines outbound SMTP TLS verification behavior.
type TLSPolicy int

const (
	// TLSPolicyOpportunistic attempts TLS when available but does not
	// require successful certificate verification. Connections are still
	// encrypted when TLS is used, but unverified certificates are accepted.
	// This is the default and matches the behavior of most production MTAs.
	TLSPolicyOpportunistic TLSPolicy = iota

	// TLSPolicyStrict requires TLS and valid certificate verification.
	// Connections fail if the remote server does not support STARTTLS,
	// the certificate chain is invalid, the hostname does not match, or
	// the certificate is expired/self-signed/untrusted.
	TLSPolicyStrict
)

// String returns the string representation of a TLSPolicy.
func (p TLSPolicy) String() string {
	switch p {
	case TLSPolicyOpportunistic:
		return "opportunistic"
	case TLSPolicyStrict:
		return "strict"
	default:
		return "unknown"
	}
}

// ParseTLSPolicy parses a string into a TLSPolicy.
// Returns an error for unknown values.
func ParseTLSPolicy(s string) (TLSPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "opportunistic", "":
		return TLSPolicyOpportunistic, nil
	case "strict":
		return TLSPolicyStrict, nil
	default:
		return TLSPolicyOpportunistic, fmt.Errorf("unknown tls policy %q: valid values are %q and %q", s, "opportunistic", "strict")
	}
}

// BuildTLSConfig creates a tls.Config for outbound SMTP connections
// based on the policy and target server name.
func (p TLSPolicy) BuildTLSConfig(serverName string) *tls.Config {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}
	switch p {
	case TLSPolicyStrict:
		cfg.InsecureSkipVerify = false
	default:
		cfg.InsecureSkipVerify = true
	}
	return cfg
}

// verifyTLSConnection performs post-handshake certificate verification.
// In opportunistic mode, verification failures are recorded but do not
// abort the connection. Returns true when the certificate is verified,
// false when it is not (opportunistic mode only).
func (p TLSPolicy) verifyTLSConnection(state tls.ConnectionState, serverName string, roots *x509.CertPool) (bool, error) {
	switch p {
	case TLSPolicyStrict:
		return true, nil
	default:
		if len(state.PeerCertificates) == 0 {
			return false, nil
		}
		opts := x509.VerifyOptions{
			DNSName: serverName,
			Roots:   roots,
		}
		if _, err := state.PeerCertificates[0].Verify(opts); err != nil {
			return false, nil
		}
		return true, nil
	}
}

// TransportConfig holds SMTP transport settings.
type TransportConfig struct {
	ConnectTimeout  time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	MaxLineLength   int
	AttemptSTARTTLS bool
	// RequireSTARTTLS makes the transport fail the
	// attempt with TempFail=true (so the queue retries
	// rather than bouncing) when the server requires
	// STARTTLS but negotiation fails. The default is
	// true: outbound delivery should never send
	// plaintext AUTH/MAIL FROM when the peer demands
	// encryption.
	RequireSTARTTLS bool
	// TLSPolicy controls outbound TLS certificate verification.
	// Supported values: opportunistic (default), strict.
	TLSPolicy TLSPolicy
	// TLSRootCAs is an optional root CA pool for certificate verification.
	// When nil, the system root pool is used. Intended for testing.
	TLSRootCAs *x509.CertPool
}

// DefaultTransportConfig returns default transport settings.
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		ConnectTimeout:  30 * time.Second,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		MaxLineLength:   1000,
		AttemptSTARTTLS: true,
		RequireSTARTTLS: true,
		TLSPolicy:       TLSPolicyOpportunistic,
	}
}

// DeliveryResult holds the outcome of a single SMTP delivery attempt.
type DeliveryResult struct {
	Success      bool
	StatusCode   int // 0 if connection failed
	StatusMsg    string
	EnhancedCode string // SMTP enhanced status code (e.g. "5.1.1")
	TempFail     bool   // true = 4xx (retryable), false = 5xx (permanent)
	HeloOK       bool
	MailFromOK   bool
	RcptOK       bool
	DataOK       bool
	TLSUsed      bool
	TLSHandshake bool   // true if STARTTLS upgrade completed
	TLSVerified  bool   // true if TLS certificate was verified successfully
	RemoteHost   string
	RemoteIP     string
	AttemptCount int
	DurationMs   int64
	// SMTPUTF8 is set if the remote server advertised
	// the SMTPUTF8 capability. Currently recorded for
	// diagnostics; not used to change envelope
	// encoding.
	SMTPUTF8 bool
	// Capabilities captures the EHLO multiline response
	// for diagnostics — the queue worker writes the
	// relevant entries (STARTTLS, SIZE, SMTPUTF8,
	// ENHANCEDSTATUSCODES) to the attempt history so the
	// operator can see what the remote server offered.
	Capabilities []string
}

// SMTPTransport connects to remote SMTP servers and delivers messages.
type SMTPTransport struct {
	Config TransportConfig
}

func NewSMTPTransport(cfg TransportConfig) *SMTPTransport {
	return &SMTPTransport{Config: cfg}
}

// Deliver sends a message to a remote SMTP server.
//
// The expected sequence when the server advertises STARTTLS is:
//
//	connect
//	EHLO
//	read capabilities
//	STARTTLS
//	TLS handshake
//	EHLO again (the server's capabilities are reset
//	            by the TLS upgrade — encryption-related
//	            extensions like SMTPUTF8 may be
//	            re-advertised here)
//	MAIL FROM
//	RCPT TO
//	DATA
//	QUIT
//
// If STARTTLS is advertised but the server replies 454/530
// to STARTTLS (e.g. "STARTTLS not available right now"),
// the transport classifies the result as a transient
// failure so the queue retries. Plaintext MAIL FROM is
// never sent in that case — the user's mailbox provider
// may have a window of 5 minutes during which STARTTLS
// is unavailable, after which it is required again.
//
// Deliver sends a message to a remote SMTP server.
//
// For TLS certificate verification, the hostname is extracted from addr.
// Use DeliverWithTLSName to provide an explicit TLS server name.
func (t *SMTPTransport) Deliver(ctx context.Context, addr string, useTLS bool, from string, to []string, data []byte, heloHost string) *DeliveryResult {
	return t.DeliverWithTLSName(ctx, addr, useTLS, from, to, data, heloHost, "")
}

// DeliverWithTLSName is like Deliver but with an explicit TLS server name
// for certificate verification. When tlsServerName is empty, the hostname
// is extracted from addr.
func (t *SMTPTransport) DeliverWithTLSName(ctx context.Context, addr string, useTLS bool, from string, to []string, data []byte, heloHost string, tlsServerName string) *DeliveryResult {
	startTime := time.Now()
	res := &DeliveryResult{
		RemoteHost: addr,
	}

	dialer := &net.Dialer{Timeout: t.Config.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err == nil {
		defer func() {
			res.DurationMs = time.Since(startTime).Milliseconds()
		}()
	}
	if err != nil {
		res.StatusMsg = fmt.Sprintf("connect failed: %v", err)
		res.TempFail = true
		return res
	}
	defer conn.Close()

	if useTLS {
		serverName := tlsServerName
		if serverName == "" {
			if h, _, err := net.SplitHostPort(addr); err == nil {
				serverName = h
			}
		}
		tlsCfg := t.Config.TLSPolicy.BuildTLSConfig(serverName)
		if t.Config.TLSRootCAs != nil {
			tlsCfg.RootCAs = t.Config.TLSRootCAs
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			res.StatusMsg = fmt.Sprintf("tls handshake failed: %v", err)
			res.TempFail = true
			return res
		}
		state := tlsConn.ConnectionState()
		verified, _ := t.Config.TLSPolicy.verifyTLSConnection(state, serverName, t.Config.TLSRootCAs)
		conn = tlsConn
		res.TLSUsed = true
		res.TLSHandshake = true
		res.TLSVerified = verified
	}

	conn.SetDeadline(time.Now().Add(t.Config.ReadTimeout))
	reader := bufio.NewReader(conn)

	// Helper to capture enhanced codes.
	captureResult := func(code int, msg string) {
		res.StatusCode = code
		res.StatusMsg = msg
		if ec := ParseEnhancedCode(msg); ec != "" {
			res.EnhancedCode = ec
		}
	}

	// Read greeting.
	code, msg := readSMTPResponse(reader)
	if code != 220 {
		captureResult(code, msg)
		// 421 is the only "deferred" greeting. All
		// other 4xx/5xx at the greeting level are
		// permanent.
		res.TempFail = code == 421
		return res
	}

	// EHLO. We capture the multiline response so the
	// STARTTLS / SIZE / SMTPUTF8 / ENHANCEDSTATUSCODES
	// capabilities can be inspected below and recorded
	// on the result for the queue worker to log.
	capabilities, ehloOK := t.sendEHLO(conn, reader, heloHost)
	res.Capabilities = capabilities
	if !ehloOK {
		// EHLO was rejected. Try HELO as a fallback
		// for ancient servers. HELO has no multiline
		// response, so the capabilities list is empty.
		fallbackCode, fallbackMsg := sendSMTPCommand(conn, reader, fmt.Sprintf("HELO %s", heloHost))
		if fallbackCode != 250 {
			captureResult(fallbackCode, fallbackMsg)
			res.TempFail = fallbackCode == 421
			return res
		}
		res.Capabilities = nil
	}
	res.HeloOK = true

	// STARTTLS: if not already encrypted and the server
	// advertised STARTTLS, upgrade the connection. If
	// STARTTLS is required by the server (the next
	// command would return 5.7.0) we MUST have done
	// this — otherwise the message is misrouted.
	if !res.TLSUsed && t.Config.AttemptSTARTTLS && hasCapability(res.Capabilities, "STARTTLS") {
		// Issue STARTTLS. A 220 reply is the
		// readiness signal; the server then waits
		// for the client to begin TLS on the same
		// socket.
		startTLSCode, startTLSMsg := sendSMTPCommand(conn, reader, "STARTTLS")
		if startTLSCode != 220 {
			// STARTTLS was advertised but the
			// server refused it right now (454
			// "TLS not available", 501 "syntax
			// error", etc). The right answer is
			// to defer and retry — the server
			// may have it back in a few minutes,
			// and sending plaintext MAIL FROM
			// would only get us a 5.7.0.
			captureResult(startTLSCode, startTLSMsg)
			res.TempFail = true
			res.StatusMsg = fmt.Sprintf("starttls refused: %s", startTLSMsg)
			return res
		}
		// 220 means the server is ready. Wrap the
		// existing conn in a TLS client and run
		// the handshake. On success we replace
		// the conn and reader; on failure we
		// defer.
		serverName := tlsServerName
		if serverName == "" {
			if h, _, err := net.SplitHostPort(addr); err == nil {
				serverName = h
			}
		}
		tlsCfg := t.Config.TLSPolicy.BuildTLSConfig(serverName)
		if t.Config.TLSRootCAs != nil {
			tlsCfg.RootCAs = t.Config.TLSRootCAs
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			captureResult(0, "")
			res.StatusMsg = fmt.Sprintf("starttls handshake failed: %v", err)
			res.TempFail = true
			return res
		}
		state := tlsConn.ConnectionState()
		verified, _ := t.Config.TLSPolicy.verifyTLSConnection(state, serverName, t.Config.TLSRootCAs)
		conn = tlsConn
		reader = bufio.NewReader(conn)
		res.TLSUsed = true
		res.TLSHandshake = true
		res.TLSVerified = verified
		conn.SetDeadline(time.Now().Add(t.Config.ReadTimeout))

		// Re-EHLO after the TLS upgrade. RFC 3207
		// §4.2: "The client MUST discard any
		// knowledge obtained from the server, such
		// as the list of SMTP service extensions,
		// which was not obtained from the TLS
		// negotiation itself." The post-TLS
		// capabilities differ in practice: many
		// servers re-advertise SMTPUTF8, AUTH,
		// and SIZE only after encryption.
		postCaps, postEhloOK := t.sendEHLO(conn, reader, heloHost)
		res.Capabilities = postCaps
		if !postEhloOK {
			captureResult(0, "")
			res.StatusMsg = "ehlo after starttls rejected"
			res.TempFail = true
			return res
		}
	}

	// If the server is going to require STARTTLS, and
	// we did not do it, fail-fast with TempFail so the
	// queue retries and the operator can see the
	// diagnostic.
	if t.Config.RequireSTARTTLS && !res.TLSUsed {
		res.StatusMsg = "starttls required but not advertised; deferring"
		res.TempFail = true
		return res
	}

	// MAIL FROM
	fromCmd := fmt.Sprintf("MAIL FROM:<%s>", from)
	if from == "" {
		fromCmd = "MAIL FROM:<>"
	}
	if code, msg := sendSMTPCommand(conn, reader, fromCmd); code != 250 {
		captureResult(code, msg)
		res.TempFail = code >= 400 && code < 500
		// The server may have rejected MAIL FROM
		// specifically because STARTTLS is
		// required ("5.7.0 Must issue STARTTLS
		// first"). Even though that is a 5xx
		// reply, classifying it as a permanent
		// bounce is wrong: it is a configuration
		// problem on our side, not a recipient
		// problem. Convert the result to a
		// transient failure so the queue retries
		// and the operator can fix the SMTP
		// client config.
		if isStartTLSRequired(code, msg) {
			res.TempFail = true
			res.StatusMsg = fmt.Sprintf("server requires STARTTLS: %s", msg)
		}
		return res
	}
	res.MailFromOK = true

	// RCPT TO
	for _, rcpt := range to {
		code, msg := sendSMTPCommand(conn, reader, fmt.Sprintf("RCPT TO:<%s>", rcpt))
		if code != 250 {
			captureResult(code, msg)
			res.TempFail = code >= 400 && code < 500
			return res
		}
	}
	res.RcptOK = true

	// DATA
	if code, msg := sendSMTPCommand(conn, reader, "DATA"); code != 354 {
		captureResult(code, msg)
		res.TempFail = code >= 400 && code < 500
		return res
	}

	// Send message body.
	conn.SetDeadline(time.Now().Add(t.Config.WriteTimeout))
	_, err = conn.Write(data)
	if err != nil {
		res.StatusMsg = fmt.Sprintf("write data: %v", err)
		res.TempFail = true
		return res
	}
	// Send terminator.
	_, err = conn.Write([]byte("\r\n.\r\n"))
	if err != nil {
		res.StatusMsg = fmt.Sprintf("write terminator: %v", err)
		res.TempFail = true
		return res
	}

	conn.SetDeadline(time.Now().Add(t.Config.ReadTimeout))
	respCode, respMsg := readSMTPResponse(reader)
	res.StatusCode = respCode

	captureResult(respCode, respMsg)
	res.TempFail = respCode >= 400 && respCode < 500
	if respCode >= 200 && respCode < 300 {
		res.Success = true
		res.DataOK = true
	}

	// QUIT
	sendSMTPCommand(conn, reader, "QUIT")
	return res
}

// sendEHLO sends EHLO and returns the multiline capability
// list plus a boolean indicating success.
//
// Per RFC 5321 §4.1.1.1, multiline responses use the
// dash ("-") between the code and the text on continuation
// lines and a space (" ") on the final line. Our
// readSMTPResponse already reads until the final line
// (space at position 3) and returns the joined text —
// so the capabilities are spread across the message
// text, separated by spaces. We split on whitespace and
// drop the first token (the human-readable hostname)
// to get the capability keyword list.
func (t *SMTPTransport) sendEHLO(conn net.Conn, reader *bufio.Reader, heloHost string) ([]string, bool) {
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte("EHLO " + heloHost + "\r\n")); err != nil {
		return nil, false
	}
	// Read the multiline response. readSMTPResponse
	// joins continuation lines into a single string.
	code, msg := readSMTPResponse(reader)
	if code != 250 {
		return nil, false
	}
	// Parse the capability list. The first word is
	// typically the helo host; everything after is a
	// capability keyword (optionally followed by
	// parameters). We keep the keyword only.
	fields := strings.Fields(msg)
	caps := make([]string, 0, len(fields))
	for _, f := range fields {
		// Strip ESMTP keyword parameters (e.g.
		// "SIZE 10240000" → "SIZE").
		if i := strings.IndexAny(f, "="); i > 0 {
			f = f[:i]
		}
		caps = append(caps, strings.ToUpper(f))
	}
	return caps, true
}

// hasCapability reports whether the EHLO capabilities
// include the given keyword. Match is case-insensitive.
func hasCapability(caps []string, name string) bool {
	want := strings.ToUpper(name)
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// isStartTLSRequired reports whether an SMTP reply
// indicates the server requires STARTTLS before further
// commands. The pattern is well-known: enhanced status
// 5.7.0 with a "Must issue STARTTLS first" text, or the
// legacy "530 Must issue STARTTLS first". We match
// case-insensitively on the substring to be tolerant of
// server-specific phrasings.
func isStartTLSRequired(code int, msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "starttls") {
		return false
	}
	// The text fragment "must issue" is the common
	// denominator across Postfix / Exim / Sendmail /
	// Exchange.
	return strings.Contains(lower, "must issue")
}

func sendSMTPCommand(conn net.Conn, reader *bufio.Reader, cmd string) (int, string) {
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	_, err := conn.Write([]byte(cmd + "\r\n"))
	if err != nil {
		return 0, fmt.Sprintf("write error: %v", err)
	}
	return readSMTPResponse(reader)
}

func readSMTPResponse(reader *bufio.Reader) (int, string) {
	lines := ""
	code := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if code > 0 {
				return code, strings.TrimSpace(lines)
			}
			return 0, fmt.Sprintf("read error: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")

		if len(line) < 3 {
			continue
		}

		// Parse response code.
		c := 0
		for i := 0; i < 3; i++ {
			if line[i] >= '0' && line[i] <= '9' {
				c = c*10 + int(line[i]-'0')
			}
		}
		if code == 0 {
			code = c
		}

		// Get the message text (skip the code and separator).
		if len(line) > 4 {
			lines += line[4:] + " "
		}

		// Check if this is the last line (space at position 3).
		if len(line) >= 4 && line[3] == ' ' {
			break
		}
	}
	return code, strings.TrimSpace(lines)
}
