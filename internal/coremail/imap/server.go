package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// Authenticator wraps the IdentityService for IMAP LOGIN.
type Authenticator struct {
	engine    interface { AuthenticateMailbox(ctx interface{}, username, password string) (interface{}, error) }
	users     sync.Map // cache: username -> mailboxID
}

// NewAuthenticator creates an IMAP authenticator.
func NewAuthenticator() *Authenticator {
	return &Authenticator{}
}

// SetEngine sets the engine for real mailbox authentication.
func (a *Authenticator) SetEngine(eng interface {
	AuthenticateMailbox(ctx interface{}, username, password string) (interface{}, error)
}) {
	a.engine = eng
}

// Authenticate verifies credentials and returns the mailbox ID.
func (a *Authenticator) Authenticate(username, password string) (uint, bool) {
	// Check cache first.
	if id, ok := a.users.Load(username); ok {
		if mid, ok := id.(uint); ok {
			return mid, true
		}
	}

	if a.engine == nil {
		return 0, false
	}

	// IdentityService.AuthenticateMailbox takes (ctx, username, password) and returns (*Mailbox, error)
	// We use interface{} to avoid direct import of coremail.Engine.
	result, err := a.engine.AuthenticateMailbox(nil, username, password)
	if err != nil || result == nil {
		return 0, false
	}

	// Extract mailbox ID via Mailbox struct.
	type mailboxIfce interface{ GetID() uint }
	if m, ok := result.(mailboxIfce); ok {
		mid := m.GetID()
		a.users.Store(username, mid)
		return mid, true
	}

	return 0, false
}

// Server represents an IMAP server instance.
type Server struct {
	Config       Config
	MailStore    *storage.MailStore
	Auth         AuthBackend
	Observability *observability.Observability

	mu       sync.Mutex
	sessions map[string]*Session
	listener net.Listener
	done     chan struct{}
	wg       sync.WaitGroup

	// listenerCb is called after the real listener is created
	// (with or without TLS) or on bind failure. Used by the
	// admin runtime telemetry (ADMIN-LISTENER-TRACKING-2C).
	listenerCb func(addr string, err error)
}

// NewServer creates an IMAP server.
func NewServer(cfg Config, ms *storage.MailStore, auth AuthBackend) *Server {
	return &Server{
		Config:    cfg,
		MailStore: ms,
		Auth:      auth,
		sessions:  make(map[string]*Session),
		done:      make(chan struct{}),
	}
}

// SetListenerCallback registers a callback that is invoked after
// the server's listener is created (or fails to bind). The
// callback receives the address string and any bind error.
func (s *Server) SetListenerCallback(cb func(addr string, err error)) {
	s.listenerCb = cb
}

// ListenAndServe starts the IMAP server with optional TLS.
func (s *Server) ListenAndServe(addr string) error {
	var listener net.Listener
	var err error

	if s.Config.TLSCertFile != "" && s.Config.TLSKeyFile != "" {
		cert, cerr := tls.LoadX509KeyPair(s.Config.TLSCertFile, s.Config.TLSKeyFile)
		if cerr != nil {
			err = fmt.Errorf("IMAP TLS cert load: %w", cerr)
			if s.listenerCb != nil {
				s.listenerCb(addr, err)
			}
			return err
		}
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
		listener, err = tls.Listen("tcp", addr, tlsCfg)
	} else {
		listener, err = net.Listen("tcp", addr)
	}
	if err != nil {
		if s.listenerCb != nil {
			s.listenerCb(addr, err)
		}
		return fmt.Errorf("imap listen: %w", err)
	}
	s.listener = listener
	if s.listenerCb != nil {
		s.listenerCb(addr, nil)
	}
	return s.serve()
}

// SetListener is a pre-existing test helper. Production startup
// uses SetListenerCallback instead. ListenAndServe always binds
// its own listener to preserve TLS configuration.
func (s *Server) SetListener(l net.Listener) {
	s.listener = l
}

func (s *Server) Serve() error {
	return s.serve()
}

// imapStopTimeout caps how long Stop() will wait on s.wg. The
// session-goroutine drain is normally fast because we close every
// active session conn; this cap is a safety net for the BLOCKER-2
// flakes — if a session goroutine is wedged for any reason
// (stuck TLS handshake, blocked syscall, etc.) Stop() returns
// instead of hanging the caller's goroutine.
const imapStopTimeout = 3 * time.Second

// Stop gracefully shuts down the IMAP server.
func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	// Close all active connections to unblock readers.
	s.mu.Lock()
	for _, sess := range s.sessions {
		if sess.conn != nil {
			sess.conn.Close()
		}
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(imapStopTimeout):
		// a session goroutine is wedged; give up draining and
		// let the caller move on rather than wedge the cleanup
		// path of the test harness.
	}
}

func (s *Server) serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
				return fmt.Errorf("imap accept: %w", err)
			}
		}

		// Check session limit.
		s.mu.Lock()
		if len(s.sessions) >= s.Config.MaxSessions {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		s.mu.Unlock()

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	session := NewSession(remoteAddr)
	session.MailStore = s.MailStore
	session.conn = conn
	session.RequireTLS = s.Config.RequireTLSForAuth

	// Detect if TLS is already active (TLS listener).
	if _, ok := conn.(*tls.Conn); ok {
		session.TLSActive = true
	}

	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()

	if s.Observability != nil {
		s.Observability.Metrics.IncIMAPSessionCreated()
		s.Observability.EventHistory.Record(observability.EventIMAPSessionCreated, map[string]string{
			"remote_ip": remoteAddr,
		})
	}

	defer func() {
		s.mu.Lock()
		delete(s.sessions, session.ID)
		s.mu.Unlock()
		if s.Observability != nil {
			s.Observability.EventHistory.Record(observability.EventIMAPSessionClosed, map[string]string{
				"remote_ip": remoteAddr,
			})
		}
	}()

	conn.SetDeadline(time.Now().Add(s.Config.ReadTimeout))
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// Send IMAP greeting.
	fmt.Fprintf(writer, "* OK [CAPABILITY %s] Orvix IMAP server ready\r\n", capabilities)
	writer.Flush()

	ctx := context.Background()

	for {
		select {
		case <-s.done:
			fmt.Fprint(writer, "* BYE IMAP server shutting down\r\n")
			writer.Flush()
			return
		default:
		}

		if session.State == StateLogout {
			return
		}

		conn.SetDeadline(time.Now().Add(s.Config.ReadTimeout))
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		cmd, err := ParseCommand(line)
		if err != nil {
			fmt.Fprint(writer, BAD("*", err.Error()))
			writer.Flush()
			continue
		}

		response := Handle(ctx, cmd, session, s.Auth)
		fmt.Fprint(writer, response)
		writer.Flush()

		if s.Observability != nil {
			switch cmd.Name {
			case "LOGIN":
				if session.State == StateAuthenticated {
					s.Observability.Metrics.IncIMAPLoginSuccess()
					s.Observability.EventHistory.Record(observability.EventIMAPLoginSuccess, map[string]string{
						"username": session.Username, "remote_ip": remoteAddr,
					})
				} else {
					s.Observability.Metrics.IncIMAPLoginFailure()
					s.Observability.EventHistory.Record(observability.EventIMAPLoginFailure, map[string]string{
						"remote_ip": remoteAddr,
					})
				}
			case "SELECT":
				if session.State == StateSelected {
					s.Observability.Metrics.IncIMAPMailboxSelected()
					s.Observability.EventHistory.Record(observability.EventIMAPMailboxSelected, map[string]string{
						"mailbox": cmd.Arguments, "username": session.Username,
					})
				}
			}
		}
	}
}
