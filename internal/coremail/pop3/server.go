package pop3

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// Authenticator wraps the authentication backend.
type Authenticator struct {
	backend AuthBackend
}

func NewAuthenticator(backend AuthBackend) *Authenticator {
	return &Authenticator{backend: backend}
}

func (a *Authenticator) Authenticate(username, password string) (uint, bool) {
	if a.backend == nil {
		return 0, false
	}
	return a.backend.Authenticate(username, password)
}

// Server represents a POP3 server.
type Server struct {
	Config        Config
	MailStore     *storage.MailStore
	Auth          *Authenticator
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

func NewServer(cfg Config, ms *storage.MailStore, auth *Authenticator) *Server {
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

func (s *Server) ListenAndServe(addr string) error {
	var listener net.Listener
	var err error

	if s.Config.TLSCertFile != "" && s.Config.TLSKeyFile != "" {
		cert, cerr := tls.LoadX509KeyPair(s.Config.TLSCertFile, s.Config.TLSKeyFile)
		if cerr != nil {
			err = fmt.Errorf("POP3 TLS cert load: %w", cerr)
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
		return fmt.Errorf("pop3 listen: %w", err)
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

// pop3StopTimeout caps how long Stop() will wait on s.wg. The
// session-goroutine drain is normally fast because we close every
// active session conn; this cap is a safety net for the BLOCKER-2
// flakes — if a session goroutine is wedged for any reason Stop()
// returns instead of hanging the caller's goroutine.
const pop3StopTimeout = 3 * time.Second

func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
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
	case <-time.After(pop3StopTimeout):
		// session goroutine wedged; give up draining and let
		// the caller move on rather than wedge the cleanup
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
				return fmt.Errorf("pop3 accept: %w", err)
			}
		}
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

	if _, ok := conn.(*tls.Conn); ok {
		session.TLSActive = true
	}

	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()

	if s.Observability != nil {
		s.Observability.Metrics.IncPOP3Session()
		s.Observability.EventHistory.Record(observability.EventIMAPSessionCreated, map[string]string{
			"protocol": "pop3", "remote_ip": remoteAddr,
		})
	}

	defer func() {
		// On disconnect, restore deleted messages (per POP3 spec: only QUIT commits).
		// The session is already cleaned up.
		s.mu.Lock()
		delete(s.sessions, session.ID)
		s.mu.Unlock()
		if s.Observability != nil {
			s.Observability.EventHistory.Record(observability.EventIMAPSessionClosed, map[string]string{
				"protocol": "pop3", "remote_ip": remoteAddr,
			})
		}
	}()

	conn.SetDeadline(time.Now().Add(s.Config.ReadTimeout))
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	// Send greeting.
	fmt.Fprint(writer, "+OK Orvix POP3 server ready\r\n")
	writer.Flush()

	for {
		select {
		case <-s.done:
			fmt.Fprint(writer, "+OK POP3 server shutting down\r\n")
			writer.Flush()
			return
		default:
		}

		conn.SetDeadline(time.Now().Add(s.Config.ReadTimeout))
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimRight(line, "\r\n")

		// Before handling QUIT, commit deletions so the response confirms completion.
		if strings.ToUpper(line) == "QUIT" || strings.HasPrefix(strings.ToUpper(line), "QUIT ") {
			toDelete := make([]uint, 0)
			if session.State == StateTransaction {
				for _, msg := range session.Messages {
					if session.Deleted[msg.ID] {
						toDelete = append(toDelete, msg.ID)
					}
				}
				for _, id := range toDelete {
					for retry := 0; retry < 3; retry++ {
						if err := s.MailStore.PurgeMessage(context.TODO(), id, nil); err == nil {
							break
						}
						time.Sleep(50 * time.Millisecond)
					}
				}
			}
			fmt.Fprint(writer, "+OK POP3 server signing off\r\n")
			writer.Flush()
			return
		}

		resp := handleCommand(line, session, s.Auth, s.MailStore, context.TODO())
		fmt.Fprint(writer, resp)
		writer.Flush()

		// Observability.
		if s.Observability != nil {
			observeCommand(s.Observability, line, resp, session)
		}
	}
}

func observeCommand(obs *observability.Observability, line, resp string, session *Session) {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])

	switch cmd {
	case "USER", "PASS":
		if strings.HasPrefix(resp, "+OK") {
			obs.Metrics.IncPOP3LoginSuccess()
			obs.EventHistory.Record(observability.EventSMTPAuthSuccess, map[string]string{
				"protocol": "pop3", "username": session.Username,
			})
		} else if strings.HasPrefix(resp, "-ERR") {
			obs.Metrics.IncPOP3LoginFailure()
			obs.EventHistory.Record(observability.EventSMTPAuthFailure, map[string]string{
				"protocol": "pop3",
			})
		}
	case "RETR":
		if strings.HasPrefix(resp, "+OK") {
			obs.Metrics.IncPOP3MessageRetrieved()
		}
	case "DELE":
		if strings.HasPrefix(resp, "+OK") {
			obs.Metrics.IncPOP3MessageDeleted()
		}
	}
}

func handleCommand(line string, session *Session, auth *Authenticator, ms *storage.MailStore, ctx context.Context) string {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	switch cmd {
	case "USER":
		return handleUSER(args, session, auth)
	case "PASS":
		return handlePASS(args, session, auth, ms, ctx)
	case "STAT":
		return handleSTAT(session)
	case "LIST":
		return handleLIST(args, session)
	case "RETR":
		return handleRETR(args, session, ctx)
	case "DELE":
		return handleDELE(args, session)
	case "UIDL":
		return handleUIDL(args, session)
	case "TOP":
		return handleTOP(args, session, ctx)
	case "NOOP":
		return Response("+OK", "")
	case "RSET":
		return handleRSET(session)
	case "QUIT":
		return handleQUIT(session)
	default:
		return Response("-ERR", "Unknown command")
	}
}

func handleUSER(args string, session *Session, auth *Authenticator) string {
	if session.State != StateAuthorization {
		return Response("-ERR", "Already authenticated")
	}
	if session.RequireTLS && !session.TLSActive {
		return Response("-ERR", "TLS required for authentication")
	}
	if args == "" {
		return Response("-ERR", "USER requires username")
	}
	session.Username = args
	return Response("+OK", "User accepted")
}

func handlePASS(args string, session *Session, auth *Authenticator, ms *storage.MailStore, ctx context.Context) string {
	if session.State != StateAuthorization {
		return Response("-ERR", "Already authenticated")
	}
	if session.RequireTLS && !session.TLSActive {
		return Response("-ERR", "TLS required for authentication")
	}
	if session.Username == "" {
		return Response("-ERR", "USER required before PASS")
	}
	if args == "" {
		return Response("-ERR", "PASS requires password")
	}

	mailboxID, ok := auth.Authenticate(session.Username, args)
	if !ok {
		return Response("-ERR", "Authentication failed")
	}

	session.MailboxID = mailboxID
	session.MailStore = ms

	// Load messages for the INBOX folder.
	folder, err := ms.Folders.GetByPath(ctx, mailboxID, "INBOX", nil)
	if err != nil || folder == nil {
		return Response("-ERR", "Mailbox not found")
	}

	total, err := ms.Messages.CountByFolder(ctx, folder.ID, nil)
	if err != nil {
		return Response("-ERR", "Cannot list messages")
	}
	msgs, _, err := ms.Messages.List(ctx, storage.MessageFilter{
		MailboxID: mailboxID,
		FolderID:  &folder.ID,
		Limit:     int(total),
	}, nil)
	if err != nil {
		return Response("-ERR", "Cannot list messages")
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	session.Messages = msgs
	session.Deleted = make(map[uint]bool)
	session.State = StateTransaction
	return Response("+OK", "Mailbox ready")
}

func handleSTAT(session *Session) string {
	if session.State != StateTransaction {
		return Response("-ERR", "Not authenticated")
	}
	total := len(session.Messages)
	var size int64
	for _, m := range session.Messages {
		if !session.Deleted[m.ID] {
			size += m.SizeBytes
		}
	}
	return fmt.Sprintf("+OK %d %d\r\n", total, size)
}

func handleLIST(args string, session *Session) string {
	if session.State != StateTransaction {
		return Response("-ERR", "Not authenticated")
	}

	if args == "" {
		// LIST with no args: return all messages.
		var resp strings.Builder
		resp.WriteString("+OK\r\n")
		for i, m := range session.Messages {
			if !session.Deleted[m.ID] {
				resp.WriteString(fmt.Sprintf("%d %d\r\n", i+1, m.SizeBytes))
			}
		}
		resp.WriteString(".\r\n")
		return resp.String()
	}

	// LIST with message number.
	msgNum := parseMsgNum(args, len(session.Messages))
	if msgNum < 1 || msgNum > len(session.Messages) {
		return Response("-ERR", "No such message")
	}
	m := session.Messages[msgNum-1]
	if session.Deleted[m.ID] {
		return Response("-ERR", "Message deleted")
	}
	return fmt.Sprintf("+OK %d %d\r\n", msgNum, m.SizeBytes)
}

func handleRETR(args string, session *Session, ctx context.Context) string {
	if session.State != StateTransaction {
		return Response("-ERR", "Not authenticated")
	}

	msgNum := parseMsgNum(args, len(session.Messages))
	if msgNum < 1 || msgNum > len(session.Messages) {
		return Response("-ERR", "No such message")
	}

	m := session.Messages[msgNum-1]
	if session.Deleted[m.ID] {
		return Response("-ERR", "Message deleted")
	}

	_, data, err := session.MailStore.LoadMessageByMessageID(ctx, m.MessageID)
	if err != nil || data == nil {
		return Response("-ERR", "Cannot retrieve message")
	}

	return fmt.Sprintf("+OK %d octets\r\n%s\r\n.\r\n", len(data), dotStuff(string(data)))
}

func handleDELE(args string, session *Session) string {
	if session.State != StateTransaction {
		return Response("-ERR", "Not authenticated")
	}

	msgNum := parseMsgNum(args, len(session.Messages))
	if msgNum < 1 || msgNum > len(session.Messages) {
		return Response("-ERR", "No such message")
	}

	m := session.Messages[msgNum-1]
	if session.Deleted[m.ID] {
		return Response("-ERR", "Message already deleted")
	}

	session.Deleted[m.ID] = true
	return Response("+OK", "Message marked for deletion")
}

func handleRSET(session *Session) string {
	if session.State != StateTransaction {
		return Response("-ERR", "Not authenticated")
	}
	session.Deleted = make(map[uint]bool)
	return Response("+OK", "Reset completed")
}

func handleQUIT(session *Session) string {
	if session.State == StateAuthorization {
		return Response("+OK", "POP3 server signing off")
	}
	session.State = StateUpdate
	return Response("+OK", "POP3 server signing off")
}

func handleUIDL(args string, session *Session) string {
	if session.State != StateTransaction {
		return Response("-ERR", "Not authenticated")
	}

	if args == "" {
		// UIDL with no args: return all messages.
		var resp strings.Builder
		resp.WriteString("+OK\r\n")
		for i, m := range session.Messages {
			if !session.Deleted[m.ID] {
				uid := messageUID(m)
				resp.WriteString(fmt.Sprintf("%d %s\r\n", i+1, uid))
			}
		}
		resp.WriteString(".\r\n")
		return resp.String()
	}

	// UIDL <n>: return specific message.
	msgNum := parseMsgNum(args, len(session.Messages))
	if msgNum < 1 || msgNum > len(session.Messages) {
		return Response("-ERR", "No such message")
	}
	m := session.Messages[msgNum-1]
	if session.Deleted[m.ID] {
		return Response("-ERR", "Message deleted")
	}
	uid := messageUID(m)
	return fmt.Sprintf("+OK %d %s\r\n", msgNum, uid)
}

// messageUID returns a stable, non-secret unique identifier for a message.
// Uses the Message.MessageID (UUID) as the UIDL identifier.
func messageUID(m storage.Message) string {
	if m.MessageID == "" {
		return fmt.Sprintf("ORVIX%d", m.ID)
	}
	return m.MessageID
}

func handleTOP(args string, session *Session, ctx context.Context) string {
	if session.State != StateTransaction {
		return Response("-ERR", "Not authenticated")
	}

	// Parse: TOP <n> <lines>
	parts := strings.Fields(args)
	if len(parts) < 2 {
		return Response("-ERR", "TOP requires message number and line count")
	}

	msgNum := parseMsgNum(parts[0], len(session.Messages))
	if msgNum < 1 || msgNum > len(session.Messages) {
		return Response("-ERR", "No such message")
	}

	m := session.Messages[msgNum-1]
	if session.Deleted[m.ID] {
		return Response("-ERR", "Message deleted")
	}

	lineCount := 0
	if _, err := fmt.Sscanf(parts[1], "%d", &lineCount); err != nil || lineCount < 0 {
		return Response("-ERR", "Invalid line count")
	}

	_, data, err := session.MailStore.LoadMessageByMessageID(ctx, m.MessageID)
	if err != nil || data == nil {
		return Response("-ERR", "Cannot retrieve message")
	}

	// Split headers and body.
	header, body := splitPOP3Body(data)

	// Get first N lines of body.
	bodyLines := strings.Split(string(body), "\n")
	if lineCount < len(bodyLines) {
		bodyLines = bodyLines[:lineCount]
	}
	bodyText := strings.Join(bodyLines, "\n")

	return fmt.Sprintf("+OK\r\n%s\r\n%s\r\n.\r\n", dotStuff(string(header)), dotStuff(bodyText))
}

// splitPOP3Body splits RFC822 data into header and body portions.
func splitPOP3Body(data []byte) ([]byte, []byte) {
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx >= 0 {
		return data[:idx+2], data[idx+4:]
	}
	idx = bytes.Index(data, []byte("\n\n"))
	if idx >= 0 {
		return data[:idx+1], data[idx+2:]
	}
	return data, nil
}

// dotStuff prefixes lines beginning with '.' with an extra '.' per RFC 1939 §3.
func dotStuff(s string) string {
	var result strings.Builder
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		if strings.HasPrefix(line, ".") {
			result.WriteString("." + line)
		} else {
			result.WriteString(line)
		}
	}
	return result.String()
}

func parseMsgNum(s string, max int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0
	}
	return n
}
