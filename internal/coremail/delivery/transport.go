package delivery

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

// TransportConfig holds SMTP transport settings.
type TransportConfig struct {
	ConnectTimeout   time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	MaxLineLength    int
	AttemptSTARTTLS  bool
}

// DefaultTransportConfig returns default transport settings.
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		ConnectTimeout:  30 * time.Second,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		MaxLineLength:   1000,
		AttemptSTARTTLS: true,
	}
}

// DeliveryResult holds the outcome of a single SMTP delivery attempt.
type DeliveryResult struct {
	Success      bool
	StatusCode   int    // 0 if connection failed
	StatusMsg    string
	EnhancedCode string // SMTP enhanced status code (e.g. "5.1.1")
	TempFail     bool   // true = 4xx (retryable), false = 5xx (permanent)
	HeloOK       bool
	MailFromOK   bool
	RcptOK       bool
	DataOK       bool
	TLSUsed      bool
	RemoteHost   string
	RemoteIP     string
	AttemptCount int
	DurationMs   int64
}

// SMTPTransport connects to remote SMTP servers and delivers messages.
type SMTPTransport struct {
	Config TransportConfig
}

func NewSMTPTransport(cfg TransportConfig) *SMTPTransport {
	return &SMTPTransport{Config: cfg}
}

// Deliver sends a message to a remote SMTP server.
func (t *SMTPTransport) Deliver(ctx context.Context, addr string, useTLS bool, from string, to []string, data []byte, heloHost string) *DeliveryResult {
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
		return res
	}
	defer conn.Close()

	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
		if err := tlsConn.Handshake(); err != nil {
			res.StatusMsg = fmt.Sprintf("tls handshake failed: %v", err)
			res.TempFail = true
			return res
		}
		conn = tlsConn
		res.TLSUsed = true
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
		return res
	}

	// EHLO
	code, msg = sendSMTPCommand(conn, reader, fmt.Sprintf("EHLO %s", heloHost))
	if code != 250 {
		code, msg = sendSMTPCommand(conn, reader, fmt.Sprintf("HELO %s", heloHost))
		if code != 250 {
			captureResult(code, msg)
			return res
		}
	}
	res.HeloOK = true

	// STARTTLS if not already encrypted and server advertised it.
	if !res.TLSUsed && t.Config.AttemptSTARTTLS {
		code, _ := sendSMTPCommand(conn, reader, "STARTTLS")
		if code == 220 {
			tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
			if err := tlsConn.Handshake(); err == nil {
				conn = tlsConn
				reader = bufio.NewReader(conn)
				res.TLSUsed = true
				// Re-EHLO after STARTTLS
				sendSMTPCommand(conn, reader, fmt.Sprintf("EHLO %s", heloHost))
			}
		}
	}

	// MAIL FROM
	fromCmd := fmt.Sprintf("MAIL FROM:<%s>", from)
	if from == "" {
		fromCmd = "MAIL FROM:<>"
	}
	if code, msg := sendSMTPCommand(conn, reader, fromCmd); code != 250 {
		captureResult(code, msg)
		res.TempFail = code >= 400 && code < 500
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
