package smtp

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/observability"
)

// Server represents an SMTP server instance.
type Server struct {
	Config            Config
	TLSConfig         *tls.Config
	Handler           *CommandHandler
	Receiver          *Receiver
	RecipientValidator RecipientValidator
	Observability     *observability.Observability

	mu         sync.Mutex
	sessions   map[string]*Session
	listener   net.Listener
	done       chan struct{}

	localDomainChecker func(ctx context.Context, domain string) (bool, error)
}



// NewServer creates a new SMTP server.
func NewServer(cfg Config, handler *CommandHandler, receiver *Receiver) *Server {
	return &Server{
		Config:   cfg,
		Handler:  handler,
		Receiver: receiver,
		sessions: make(map[string]*Session),
		done:     make(chan struct{}),
	}
}

// ListenAndServe starts the SMTP server on the given address.
func (s *Server) ListenAndServe(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp listen: %w", err)
	}
	s.listener = listener
	return s.serve()
}

func (s *Server) serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
				return fmt.Errorf("smtp accept: %w", err)
			}
		}
		go s.handleConn(conn)
	}
}

// LoadTLSConfig creates a tls.Config from the server's config.
// Returns nil if no cert/key files are configured.
func LoadTLSConfig(cfg Config) (*tls.Config, error) {
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		ClientAuth:   tls.NoClientCert,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
	}, nil
}

// LoadTLSConfigWithCert creates a tls.Config from an injected certificate (for testing).
func LoadTLSConfigWithCert(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		ClientAuth:   tls.NoClientCert,
	}
}

// SetLocalDomainChecker sets the local domain checker for relay protection.
func (s *Server) SetLocalDomainChecker(fn func(ctx context.Context, domain string) (bool, error)) {
	s.localDomainChecker = fn
}

// Stop gracefully stops the SMTP server.
func (s *Server) Stop() error {
	close(s.done)
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	session := NewSession(remoteAddr, s.TLSConfig, s.Config)

	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()

	if s.Observability != nil {
		s.Observability.Metrics.IncSMTPSessions()
	}

	defer func() {
		s.mu.Lock()
		delete(s.sessions, session.ID)
		s.mu.Unlock()
	}()

	conn.SetDeadline(time.Now().Add(s.Config.ReadTimeout))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// Send greeting.
	writeResponse(writer, ResponseReady)
	writer.Flush()

	handler := NewCommandHandler(s.Config, s.Handler.auth, session)
	if s.RecipientValidator != nil {
		handler.SetRecipientValidator(s.RecipientValidator)
	}
	if s.localDomainChecker != nil {
		handler.SetLocalDomainChecker(s.localDomainChecker)
	}

	for {
		if session.State == StateClosed {
			return
		}

		line, err := readLine(reader, s.Config.MaxLineLength)
		if err != nil {
			if err == io.EOF {
				return
			}
			writeResponse(writer, responsef(StatusBadArgs, "5.5.2 %s", err.Error()))
			writer.Flush()
			continue
		}

		cmd, err := ParseLine(line, s.Config.MaxLineLength)
		if err != nil {
			writeResponse(writer, responsef(StatusBadArgs, "5.5.2 %s", err.Error()))
			writer.Flush()
			continue
		}

		// Handle AUTH LOGIN multi-step.
		if handler.authStep > 0 && cmd.Verb != "AUTH" && cmd.Verb != "QUIT" && cmd.Verb != "RSET" {
			resp := handler.HandleAuthLoginStep(context.Background(), line)
			writeResponse(writer, resp)
			writer.Flush()
			continue
		}

		resp := handler.Handle(context.Background(), cmd)
		writeResponse(writer, resp)
		writer.Flush()

		// Handle STARTTLS upgrade.
		if cmd.Verb == "STARTTLS" && resp.Code == 220 && session.TLSConfig != nil && !session.TLSActive {
			tlsConn := tls.Server(conn, session.TLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				writeResponse(writer, responsef(StatusTLSFailed, "5.7.0 TLS handshake failed"))
				writer.Flush()
				if s.Observability != nil {
					s.Observability.EventHistory.Record(observability.EventSTARTTLSFailure,
						map[string]string{"remote_ip": session.RemoteAddr})
				}
				session.State = StateClosed
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)
			session.TLSActive = true
			session.State = StateNew
			session.Authenticated = false
			session.AuthUser = ""
			session.AuthIdentity = nil
			if s.Observability != nil {
				s.Observability.Metrics.IncTLSUpgrade()
				s.Observability.EventHistory.Record(observability.EventSTARTTLSSuccess,
					map[string]string{"remote_ip": session.RemoteAddr})
			}
			writeResponse(writer, ResponseReady)
			writer.Flush()
			continue
		}

		// Handle DATA state.
		if session.State == StateData {
			data, err := readData(reader, s.Config.MaxMessageSizeBytes, s.Config.MaxLineLength)
			if err != nil {
				writeResponse(writer, responsef(StatusMailboxUnavailable, "4.3.0 %s", err.Error()))
				session.State = StateGreeted
				session.ResetTransaction()
				writer.Flush()
				continue
			}

			session.DataBuffer = data

			if s.Receiver != nil {
				var acceptErr error
				func() {
					defer func() {
						if r := recover(); r != nil {
							acceptErr = fmt.Errorf("internal accept error")
						}
					}()
					acceptErr = s.Receiver.AcceptMessage(context.Background(), session)
				}()
				if acceptErr != nil {
					writeResponse(writer, responsef(StatusMailboxUnavailable, "4.3.0 internal error - try again later"))
					session.State = StateGreeted
					session.ResetTransaction()
					writer.Flush()
					continue
				}
			}

			writeResponse(writer, MessageAccepted)
			writer.Flush()
			session.State = StateGreeted
			session.ResetTransaction()
		}
	}
}

func readLine(reader *bufio.Reader, maxLen int) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) > maxLen+2 { // +2 for CRLF
		return "", fmt.Errorf("line length %d exceeds maximum %d", len(line), maxLen)
	}
	return line, nil
}

func readData(reader *bufio.Reader, maxSize int64, maxLineLen int) ([]byte, error) {
	var buf strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read data: %w", err)
		}

		// Check for terminator: <CRLF>.<CRLF> or <LF>.<LF>
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			break
		}

		// Unescape dot-stuffed lines.
		if strings.HasPrefix(line, ".") && len(line) > 1 {
			line = line[1:]
		}

		buf.WriteString(line)

		if maxSize > 0 && int64(buf.Len()) > maxSize {
			return nil, fmt.Errorf("data exceeds maximum size of %d bytes", maxSize)
		}
	}
	return []byte(buf.String()), nil
}

func writeResponse(writer *bufio.Writer, resp Response) {
	if resp.Message == "" {
		writer.WriteString(fmt.Sprintf("%d\r\n", resp.Code))
		return
	}
	// Check if multi-line.
	if strings.Contains(resp.Message, "\r\n") {
		lines := strings.Split(resp.Message, "\r\n")
		for i, line := range lines {
			sep := "-"
			if i == len(lines)-1 {
				sep = " "
			}
			writer.WriteString(fmt.Sprintf("%d%s%s\r\n", resp.Code, sep, line))
		}
		return
	}
	writer.WriteString(fmt.Sprintf("%d %s\r\n", resp.Code, resp.Message))
}
