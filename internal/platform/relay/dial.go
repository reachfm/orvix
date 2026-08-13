package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
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

// totalTestTimeout bounds an entire operator-triggered connection test
// (DNS + connect + TLS handshake + command exchange) — a test that has
// not completed inside this budget is reported as a timeout instead of
// hanging the request.
const totalTestTimeout = 15 * time.Second

// TestConnection performs a real connect + EHLO + (STARTTLS if
// required) + AUTH PLAIN (if a credential is configured) + QUIT
// against a provider, WITHOUT sending any mail. This is the
// operator-triggered connection test.
//
// Security posture (mandatory for server-side connectivity tests):
//   - the target must pass ValidateRelayTarget (hostname/IP policy);
//   - DNS resolution is performed by a validating dialer that rejects
//     unsafe resolved addresses and dials the validated IP (no
//     DNS-rebinding window);
//   - the whole exchange runs under totalTestTimeout;
//   - every error is redacted and bounded before it reaches the
//     caller, an audit record, or a log line;
//   - the credential is decrypted only for the single dial and is
//     never returned, logged, or persisted.
func (s *Service) TestConnection(ctx context.Context, providerID uint) (*HealthCheckResult, error) {
	p, err := s.repo.GetProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrProviderNotFound
	}
	if err := ValidateRelayTarget(p.Host, p.Port); err != nil {
		return &HealthCheckResult{Error: redactHealthError("unsafe relay target: " + err.Error())}, nil
	}
	// (D) TestConnection follows the SAME credential-transport policy as real
	// delivery. Without this, an operator could "successfully" test a provider
	// configured for cleartext or opportunistic AUTH — leaking the credential
	// on an operator-triggered action, and reporting a green result for a
	// configuration that real delivery would refuse. The credential is not
	// decrypted at all on this path.
	if err := ValidateCredentialTransport(*p); err != nil {
		res := &HealthCheckResult{Error: "refusing SMTP AUTH without verified TLS"}
		if persistErr := s.repo.SetTestResult(ctx, providerID, s.clock.Now(), "insecure_credential_transport", p.Version); persistErr != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "persist relay connection-test result", persistErr)
		}
		return res, nil
	}
	password, err := s.decryptCredential(*p)
	if err != nil {
		return &HealthCheckResult{Error: "credential decrypt failed"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, totalTestTimeout)
	defer cancel()
	result := dialAndTest(ctx, newValidatingDialer(), *p, password, nil)
	// Connection-test state is operator-visible evidence. A failed write must
	// not be presented as a fully successful administrative operation: doing so
	// leaves the UI and audit trail disagreeing about what was actually tested.
	if err := s.repo.SetTestResult(ctx, providerID, s.clock.Now(), testResultSummary(result), p.Version); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "persist relay connection-test result", err)
	}
	return result, nil
}

// testResultSummary maps a HealthCheckResult into the safe, persisted
// one-word outcome stored on the provider row. Only this vocabulary
// ever reaches LastTestResult — never a raw error string.
func testResultSummary(r *HealthCheckResult) string {
	switch {
	case r.Connected && r.TLSNegotiated && r.AuthOK:
		return "ok"
	case !r.Connected:
		return "connect_failed"
	case r.Connected && !r.TLSNegotiated && r.AuthOK:
		// ConnSecurity none + auth ok.
		return "ok"
	case r.Connected && !r.AuthOK && r.Error == "":
		return "ok"
	case r.Error != "" && strings.Contains(strings.ToLower(r.Error), "tls"):
		return "tls_failed"
	case r.Error != "" && (strings.Contains(strings.ToLower(r.Error), "auth") || strings.Contains(strings.ToLower(r.Error), "authentication")):
		return "auth_failed"
	default:
		return "failed"
	}
}

func dialAndTest(ctx context.Context, d dialer, p Provider, password string, roots *x509.CertPool) *HealthCheckResult {
	start := time.Now()
	result := &HealthCheckResult{}
	conn, reader, err := connectAndAuth(ctx, d, p, password, result, roots)
	if err != nil {
		result.Error = redactHealthError(result.Error)
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	defer conn.Close()
	deadline := time.Now().Add(totalTestTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_, _ = smtpCommand(conn, reader, "QUIT", deadline)
	result.Error = redactHealthError(result.Error)
	result.DurationMS = time.Since(start).Milliseconds()
	return result
}

// connectAndAuth is the shared connect+EHLO+STARTTLS+AUTH sequence
// used by both the operator-triggered TestConnection health check and
// the real Deliver path — one implementation of the handshake, so the
// two can never drift (a health check that passes but a delivery that
// fails the same handshake would be a trap for operators).
func connectAndAuth(ctx context.Context, d dialer, p Provider, password string, result *HealthCheckResult, roots *x509.CertPool) (net.Conn, *bufio.Reader, error) {
	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		result.Error = "connect: " + err.Error()
		return nil, nil, err
	}
	result.Connected = true

	// F6: the dial succeeded but every subsequent read/write (greeting,
	// EHLO, STARTTLS, AUTH, MAIL/RCPT/DATA) is bounded by an absolute
	// deadline derived from the context and the per-command timeout. The
	// caller's deadline, if any, wins when it is earlier.
	handshakeDeadline := time.Now().Add(smtpCommandTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(handshakeDeadline) {
		handshakeDeadline = dl
	}
	if err := conn.SetDeadline(handshakeDeadline); err != nil {
		result.Error = "set handshake deadline: " + err.Error()
		conn.Close()
		return nil, nil, err
	}

	if p.ConnSecurity == ConnSecurityImplicitTLS {
		tlsConn := tls.Client(conn, tlsConfigFor(p, roots))
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
	caps, err := smtpCommand(conn, reader, "EHLO "+localHostForEHLO(), handshakeDeadline)
	if err != nil {
		result.Error = "ehlo: " + err.Error()
		conn.Close()
		return nil, nil, err
	}

	if p.ConnSecurity == ConnSecurityStartTLS {
		if _, err := smtpCommand(conn, reader, "STARTTLS", handshakeDeadline); err != nil {
			result.Error = "starttls: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
		tlsConn := tls.Client(conn, tlsConfigFor(p, roots))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			result.Error = "starttls handshake: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
		conn = tlsConn
		reader = bufio.NewReader(conn)
		result.TLSNegotiated = true
		caps, err = smtpCommand(conn, reader, "EHLO "+localHostForEHLO(), handshakeDeadline)
		if err != nil {
			result.Error = "ehlo after starttls: " + err.Error()
			conn.Close()
			return nil, nil, err
		}
	}

	if password != "" {
		// (D) The credential may only be transmitted over a VERIFIED TLS
		// session. This is checked against what actually happened on the wire:
		// result.TLSNegotiated is true only after a completed implicit-TLS or
		// STARTTLS handshake. It therefore also catches a server that accepted
		// the connection but never upgraded, which configuration alone cannot
		// detect. The connection is torn down without sending AUTH, so the
		// credential never reaches the socket.
		if err := requireVerifiedTLSForAuth(p, result.TLSNegotiated); err != nil {
			result.Error = "refusing SMTP AUTH without verified TLS"
			conn.Close()
			return nil, nil, err
		}
		if !capsContain(caps, "AUTH") {
			result.Error = "server does not advertise AUTH"
			conn.Close()
			return nil, nil, fmt.Errorf("no auth capability")
		}
		authStr := base64.StdEncoding.EncodeToString([]byte("\x00" + p.Username + "\x00" + password))
		if _, err := smtpCommand(conn, reader, "AUTH PLAIN "+authStr, handshakeDeadline); err != nil {
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
	Success  bool
	TempFail bool
	// Ambiguous is true when the DATA payload (including the terminating
	// dot) was written but the final response was never read: the
	// recipient MAY have received the message. The caller must NOT
	// immediately re-send through a fallback provider; the outcome needs
	// controlled reconciliation (F6).
	Ambiguous  bool
	StatusCode int
	StatusMsg  string
}

// Deliver connects to the provider and sends one message via
// MAIL FROM/RCPT TO/DATA. This is the function the outbound delivery worker
// calls once SelectRoute has chosen a non-direct route.
//
// H-4: it dials through the SAME secure validating dialer that TestConnection
// uses — never a plain net.Dial. The dialer resolves the provider hostname,
// rejects the connection if any resolved A/AAAA record is a loopback /
// private / link-local / metadata / CGNAT / reserved address, and connects
// only to the validated IP. Because the connection is refused BEFORE any TCP
// handshake, a blocked destination receives neither the AUTH credential nor a
// single message byte — connectAndAuth is never reached. This closes the
// SSRF where a tenant-controlled relay hostname resolves to an internal
// address during real delivery.
func Deliver(ctx context.Context, p Provider, password string, from string, to []string, data []byte) *DeliverResult {
	return deliverWith(ctx, newValidatingDialer(), p, password, from, to, data, nil)
}

// deliverWith is the delivery body parameterised by dialer, so the exported
// Deliver always uses the secure dialer while tests can drive the SMTP
// exchange over an in-process pipe.
func deliverWith(ctx context.Context, d dialer, p Provider, password string, from string, to []string, data []byte, roots *x509.CertPool) *DeliverResult {
	result := &HealthCheckResult{}
	conn, reader, err := connectAndAuth(ctx, d, p, password, result, roots)
	if err != nil {
		return &DeliverResult{TempFail: true, StatusMsg: redactHealthError(result.Error)}
	}
	defer conn.Close()

	// F6: bounded SMTP transaction. Every read and write on the exchange
	// below runs under a per-command deadline AND the total transaction
	// deadline, both well below the queue lease (DefaultLeaseSeconds =
	// 300s), so a hung relay can never pin a worker goroutine past its
	// lease. Deadlines are derived from the context: if the caller's
	// context is already closer than the configured bound, the context
	// wins.
	transactionDeadline := time.Now().Add(smtpTransactionTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(transactionDeadline) {
		transactionDeadline = dl
	}
	applyDeadline := func() error {
		return conn.SetDeadline(transactionDeadline)
	}
	if err := applyDeadline(); err != nil {
		return &DeliverResult{TempFail: true, StatusMsg: redactHealthError("set transaction deadline: " + err.Error())}
	}

	if _, err := smtpCommand(conn, reader, "MAIL FROM:<"+from+">", transactionDeadline); err != nil {
		return &DeliverResult{TempFail: isTempSMTPErr(err), StatusMsg: redactHealthError(err.Error())}
	}
	for _, rcpt := range to {
		if _, err := smtpCommand(conn, reader, "RCPT TO:<"+rcpt+">", transactionDeadline); err != nil {
			return &DeliverResult{TempFail: isTempSMTPErr(err), StatusMsg: redactHealthError(err.Error())}
		}
	}
	if _, err := smtpCommand(conn, reader, "DATA", transactionDeadline); err != nil {
		return &DeliverResult{TempFail: isTempSMTPErr(err), StatusMsg: redactHealthError(err.Error())}
	}
	payload := dataTerminated(data)
	if writeResult := writeDataPayload(conn, payload, transactionDeadline); writeResult != nil {
		return writeResult
	}
	dataReadDeadline := boundedDeadline(time.Now(), smtpCommandTimeout, transactionDeadline)
	if err := conn.SetReadDeadline(dataReadDeadline); err != nil {
		return &DeliverResult{TempFail: true, StatusMsg: redactHealthError(err.Error())}
	}
	code, msg, err := readSMTPLine(reader)
	if err != nil {
		// F6: AMBIGUOUS acceptance. The DATA payload — including the
		// terminating dot — has already been written and flushed to the
		// socket. A timeout or connection drop AFTER that point means we
		// cannot know whether the server accepted the message. This is
		// deliberately NOT a plain temp-fail: the caller must not
		// immediately re-send through a fallback provider, because the
		// recipient may already have received the message. The status
		// string never carries credentials or message content.
		if isTimeoutOrEOF(err) {
			return &DeliverResult{TempFail: true, Ambiguous: true, StatusMsg: redactHealthError("delivery outcome unknown after DATA; recipient may have received the message")}
		}
		return &DeliverResult{TempFail: true, StatusMsg: redactHealthError(err.Error())}
	}
	_, _ = smtpCommand(conn, reader, "QUIT", transactionDeadline)
	if code >= 200 && code < 300 {
		return &DeliverResult{Success: true, StatusCode: code, StatusMsg: msg}
	}
	return &DeliverResult{StatusCode: code, StatusMsg: msg, TempFail: code >= 400 && code < 500}
}

// writeDataPayload applies the strict transaction bound and classifies every
// stream-write outcome. A non-nil result is a delivery failure; nil means the
// complete dot-terminated payload was written.
func writeDataPayload(conn net.Conn, payload []byte, transactionDeadline time.Time) *DeliverResult {
	dataWriteDeadline := boundedDeadline(time.Now(), smtpCommandTimeout, transactionDeadline)
	if err := conn.SetWriteDeadline(dataWriteDeadline); err != nil {
		return &DeliverResult{TempFail: true, StatusMsg: redactHealthError(err.Error())}
	}
	written, err := conn.Write(payload)
	if err != nil {
		// A stream write may return n > 0 together with an error. Once any
		// DATA octets have crossed the socket boundary we cannot prove that
		// the relay rejected the transaction: its TCP stack may have received
		// the complete message even though the local write reported a reset or
		// timeout. Treat that shape as ambiguous so the caller stops the
		// fallback chain instead of risking an immediate duplicate delivery.
		if written > 0 {
			return &DeliverResult{TempFail: true, Ambiguous: true, StatusMsg: redactHealthError("delivery outcome unknown during DATA write; recipient may have received the message")}
		}
		return &DeliverResult{TempFail: true, StatusMsg: redactHealthError(err.Error())}
	}
	if written != len(payload) {
		// io.Writer is allowed to return a short write with no error. The
		// transport outcome is still unknowable because a prefix of DATA was
		// transmitted, so fail closed as ambiguous rather than retrying a
		// second provider.
		return &DeliverResult{TempFail: true, Ambiguous: true, StatusMsg: redactHealthError("delivery outcome unknown after short DATA write; recipient may have received the message")}
	}
	return nil
}

// boundedDeadline returns the earlier of the per-operation timeout and the
// transaction deadline. DATA reads/writes previously used now+30s directly,
// which could extend a nominal 90-second transaction when DATA began near the
// overall deadline.
func boundedDeadline(now time.Time, operationTimeout time.Duration, transactionDeadline time.Time) time.Time {
	operationDeadline := now.Add(operationTimeout)
	if transactionDeadline.Before(operationDeadline) {
		return transactionDeadline
	}
	return operationDeadline
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

// tlsConfigFor builds the TLS configuration for one dial.
//
// roots is nil on every production path, meaning the host trust store is used.
// It is non-nil only in tests, which need a self-signed CA to be trusted so
// the VERIFIED-TLS credential path can be exercised for real (with strict
// verification genuinely on) rather than simulated with InsecureSkipVerify.
func tlsConfigFor(p Provider, roots *x509.CertPool) *tls.Config {
	return &tls.Config{
		ServerName: p.Host,
		// Only ever true for a provider with NO credential: SelectRoute and
		// requireVerifiedTLSForAuth both refuse to authenticate over a session
		// configured this way. See credential_transport.go.
		InsecureSkipVerify: p.TLSValidation == TLSValidationOpportunistic,
		MinVersion:         tls.VersionTLS12,
		RootCAs:            roots,
	}
}

func localHostForEHLO() string { return "orvix-relay-client" }

// maxSMTPLineLen bounds a single SMTP response line and
// maxSMTPContinuationLines bounds a multi-line reply (RFC 5321
// "250-..." style). Together they make response reads bounded — a
// pathological server cannot grow memory or time unboundedly.
const (
	maxSMTPLineLen           = 8192
	maxSMTPContinuationLines = 100
	// smtpCommandTimeout bounds ONE command/response exchange.
	smtpCommandTimeout = 30 * time.Second
	// smtpTransactionTimeout bounds the ENTIRE SMTP transaction
	// (MAIL/RCPT/DATA/body/final response). It is deliberately well
	// below the queue lease (DefaultLeaseSeconds = 300s) so a hung
	// relay cannot pin a worker goroutine past its lease (F6).
	smtpTransactionTimeout = 90 * time.Second
)

// isTimeoutOrEOF reports whether an SMTP read failed because the peer
// went silent or closed after the payload was sent — the conditions
// that make the acceptance outcome ambiguous.
func isTimeoutOrEOF(err error) bool {
	if err == nil {
		return false
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}

func readSMTPLine(r *bufio.Reader) (code int, msg string, err error) {
	line, err := readBoundedLine(r)
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
		for i := 0; i < maxSMTPContinuationLines; i++ {
			next, rerr := readBoundedLine(r)
			if rerr != nil {
				return code, msg, rerr
			}
			next = strings.TrimRight(next, "\r\n")
			msg += "\n" + next
			if len(next) >= 4 && next[3] == ' ' {
				return code, msg, nil
			}
		}
		return 0, "", fmt.Errorf("response exceeds bounded multi-line limit")
	}
	return code, msg, nil
}

// readBoundedLine reads one CRLF-terminated line, refusing to buffer
// more than maxSMTPLineLen bytes even if the peer never sends a
// terminator.
func readBoundedLine(r *bufio.Reader) (string, error) {
	var b []byte
	for {
		chunk, err := r.ReadSlice('\n')
		b = append(b, chunk...)
		if err == bufio.ErrBufferFull {
			if len(b) > maxSMTPLineLen {
				return "", fmt.Errorf("response line exceeds %d bytes", maxSMTPLineLen)
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if len(b) > maxSMTPLineLen {
			return "", fmt.Errorf("response line exceeds %d bytes", maxSMTPLineLen)
		}
		return string(b), nil
	}
}

func smtpCommand(conn net.Conn, r *bufio.Reader, cmd string, deadline time.Time) ([]string, error) {
	// F6: per-command deadline, so a peer that stops answering mid-
	// exchange cannot pin the worker. The overall transaction deadline
	// (set by deliverWith / connectAndAuth) remains the harder bound:
	// the per-command window is the EARLIER of the two, so an already
	// near-expiring context always wins.
	cmdDeadline := time.Now().Add(smtpCommandTimeout)
	if deadline.Before(cmdDeadline) {
		cmdDeadline = deadline
	}
	if err := conn.SetWriteDeadline(cmdDeadline); err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte(cmd + "\r\n")); err != nil {
		return nil, err
	}
	if err := conn.SetReadDeadline(cmdDeadline); err != nil {
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
