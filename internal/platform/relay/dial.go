package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// HealthCheckResult is the outcome of an operator-triggered connection
// test — never contains the credential, only whether auth succeeded.
type HealthCheckResult struct {
	Connected     bool   `json:"connected"`
	TLSNegotiated bool   `json:"tls_negotiated"`
	AuthOK        bool   `json:"auth_ok"`
	Error         string `json:"error,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
}

// dialer abstracts the network connect step so tests can substitute
// an in-process pipe instead of a real TCP dial.
type dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

type netDialer struct{}

func (netDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, network, addr)
}

// TestConnection performs a real connect + EHLO + (STARTTLS if
// required) + AUTH PLAIN (if a credential is configured) + QUIT
// against a provider, WITHOUT sending any mail. This is the
// operator-triggered connection test.
func (s *Service) TestConnection(ctx context.Context, providerID uint) (*HealthCheckResult, error) {
	p, err := s.repo.GetProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrProviderNotFound
	}
	password, err := s.decryptCredential(*p)
	if err != nil {
		return &HealthCheckResult{Error: "credential decrypt failed"}, nil
	}
	return dialAndTest(ctx, netDialer{}, *p, password), nil
}

func dialAndTest(ctx context.Context, d dialer, p Provider, password string) *HealthCheckResult {
	start := time.Now()
	result := &HealthCheckResult{}
	conn, reader, err := connectAndAuth(ctx, d, p, password, result)
	if err != nil {
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	defer conn.Close()
	_, _ = smtpCommand(conn, reader, "QUIT")
	result.DurationMS = time.Since(start).Milliseconds()
	return result
}

// connectAndAuth is the shared connect+EHLO+STARTTLS+AUTH sequence
// used by both the operator-triggered TestConnection health check and
// the real Deliver path — one implementation of the handshake, so the
// two can never drift (a health check that passes but a delivery that
// fails the same handshake would be a trap for operators).
func connectAndAuth(ctx context.Context, d dialer, p Provider, password string, result *HealthCheckResult) (net.Conn, *bufio.Reader, error) {
	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		result.Error = "connect: " + err.Error()
		return nil, nil, err
	}
	result.Connected = true

	if p.ConnSecurity == ConnSecurityImplicitTLS {
		tlsConn := tls.Client(conn, tlsConfigFor(p))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			result.Error = "implicit tls handshake: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
		conn = tlsConn
		result.TLSNegotiated = true
	}

	reader := bufio.NewReader(conn)
	if _, _, err := readSMTPLine(reader); err != nil { // greeting
		result.Error = "greeting: " + err.Error()
		conn.Close()
		return nil, nil, err
	}
	caps, err := smtpCommand(conn, reader, "EHLO "+localHostForEHLO())
	if err != nil {
		result.Error = "ehlo: " + err.Error()
		conn.Close()
		return nil, nil, err
	}

	if p.ConnSecurity == ConnSecurityStartTLS {
		if _, err := smtpCommand(conn, reader, "STARTTLS"); err != nil {
			result.Error = "starttls: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
		tlsConn := tls.Client(conn, tlsConfigFor(p))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			result.Error = "starttls handshake: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
		conn = tlsConn
		reader = bufio.NewReader(conn)
		result.TLSNegotiated = true
		caps, err = smtpCommand(conn, reader, "EHLO "+localHostForEHLO())
		if err != nil {
			result.Error = "ehlo after starttls: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
	}

	if password != "" {
		if !capsContain(caps, "AUTH") {
			result.Error = "server does not advertise AUTH"
			conn.Close()
			return nil, nil, fmt.Errorf("no auth capability")
		}
		authStr := base64.StdEncoding.EncodeToString([]byte("\x00" + p.Username + "\x00" + password))
		if _, err := smtpCommand(conn, reader, "AUTH PLAIN "+authStr); err != nil {
			result.Error = "auth: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
		result.AuthOK = true
	}
	return conn, reader, nil
}

// DeliverResult is the outcome of an actual relay delivery attempt —
// deliberately separate from HealthCheckResult so a caller can never
// confuse "connection tested OK" with "mail was accepted".
type DeliverResult struct {
	Success    bool
	TempFail   bool
	StatusCode int
	StatusMsg  string
}

// Deliver connects to the provider (encrypting nothing further — the
// caller already holds the decrypted password for this one call) and
// sends one message via MAIL FROM/RCPT TO/DATA. This is the function
// the outbound delivery worker calls once SelectRoute has chosen a
// non-direct route.
func Deliver(ctx context.Context, p Provider, password string, from string, to []string, data []byte) *DeliverResult {
	result := &HealthCheckResult{}
	conn, reader, err := connectAndAuth(ctx, netDialer{}, p, password, result)
	if err != nil {
		return &DeliverResult{TempFail: true, StatusMsg: result.Error}
	}
	defer conn.Close()

	if _, err := smtpCommand(conn, reader, "MAIL FROM:<"+from+">"); err != nil {
		return &DeliverResult{TempFail: isTempSMTPErr(err), StatusMsg: err.Error()}
	}
	for _, rcpt := range to {
		if _, err := smtpCommand(conn, reader, "RCPT TO:<"+rcpt+">"); err != nil {
			return &DeliverResult{TempFail: isTempSMTPErr(err), StatusMsg: err.Error()}
		}
	}
	if _, err := smtpCommand(conn, reader, "DATA"); err != nil {
		return &DeliverResult{TempFail: isTempSMTPErr(err), StatusMsg: err.Error()}
	}
	payload := dataTerminated(data)
	if _, err := conn.Write(payload); err != nil {
		return &DeliverResult{TempFail: true, StatusMsg: err.Error()}
	}
	code, msg, err := readSMTPLine(reader)
	if err != nil {
		return &DeliverResult{TempFail: true, StatusMsg: err.Error()}
	}
	_, _ = smtpCommand(conn, reader, "QUIT")
	if code >= 200 && code < 300 {
		return &DeliverResult{Success: true, StatusCode: code, StatusMsg: msg}
	}
	return &DeliverResult{StatusCode: code, StatusMsg: msg, TempFail: code >= 400 && code < 500}
}

// dataTerminated appends the SMTP DATA terminator (CRLF.CRLF),
// dot-stuffing any line that begins with a literal dot per RFC 5321
// §4.5.2.
func dataTerminated(data []byte) []byte {
	lines := strings.Split(string(data), "\r\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ".") {
			lines[i] = "." + l
		}
	}
	out := strings.Join(lines, "\r\n")
	if !strings.HasSuffix(out, "\r\n") {
		out += "\r\n"
	}
	return []byte(out + ".\r\n")
}

func isTempSMTPErr(err error) bool {
	// smtpCommand only returns an error for code>=400; 4xx is temp,
	// 5xx is permanent. The formatted message carries the code.
	return strings.Contains(err.Error(), "smtp error 4")
}

func tlsConfigFor(p Provider) *tls.Config {
	return &tls.Config{
		ServerName:         p.Host,
		InsecureSkipVerify: p.TLSValidation == TLSValidationOpportunistic,
		MinVersion:         tls.VersionTLS12,
	}
}

func localHostForEHLO() string { return "orvix-relay-client" }

func readSMTPLine(r *bufio.Reader) (code int, msg string, err error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 3 {
		return 0, "", fmt.Errorf("malformed response: %q", line)
	}
	code, cerr := strconv.Atoi(line[:3])
	if cerr != nil {
		return 0, "", fmt.Errorf("malformed response code: %q", line)
	}
	msg = line
	// Multi-line response: "250-..." continues until "250 ...".
	if len(line) > 3 && line[3] == '-' {
		for {
			next, err := r.ReadString('\n')
			if err != nil {
				return code, msg, err
			}
			next = strings.TrimRight(next, "\r\n")
			msg += "\n" + next
			if len(next) >= 4 && next[3] == ' ' {
				break
			}
		}
	}
	return code, msg, nil
}

func smtpCommand(conn net.Conn, r *bufio.Reader, cmd string) ([]string, error) {
	if _, err := conn.Write([]byte(cmd + "\r\n")); err != nil {
		return nil, err
	}
	code, msg, err := readSMTPLine(r)
	if err != nil {
		return nil, err
	}
	if code >= 400 {
		return nil, fmt.Errorf("smtp error %d: %s", code, msg)
	}
	var lines []string
	for _, l := range strings.Split(msg, "\n") {
		if len(l) > 4 {
			lines = append(lines, strings.ToUpper(strings.TrimSpace(l[4:])))
		}
	}
	return lines, nil
}

func capsContain(caps []string, want string) bool {
	for _, c := range caps {
		if strings.HasPrefix(c, want) {
			return true
		}
	}
	return false
}
