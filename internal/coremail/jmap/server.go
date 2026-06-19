package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// ── Server ───────────────────────────────────────────────────

func NewServer(eng *coremail.Engine) *Server {
	s := &Server{
		Engine:   eng,
		Hostname: "localhost",
		mux:      http.NewServeMux(),
		done:     make(chan struct{}),
	}
	s.registerRoutes()
	return s
}

func (s *Server) SetMailStore(ms *storage.MailStore) {
	s.MailStore = ms
}

func (s *Server) SetAllowedOrigins(origins []string) {
	s.AllowedOrigins = sanitizeAllowedOrigins(origins)
}

// SetListener assigns a pre-bound net.Listener to the server.
// When set, ListenAndServe will use this listener instead of
// creating a new one. This is infrastructure used by the admin
// runtime telemetry so the listener registry can confirm a
// successful bind before the server starts accepting connections.
func (s *Server) SetListener(l net.Listener) {
	s.customListener = l
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/jmap/session", s.handleSession)
	s.mux.HandleFunc("/.well-known/jmap", s.handleWellKnown)
	s.mux.HandleFunc("/jmap/api", s.handleAPI)
	s.mux.HandleFunc("/jmap/download/", s.handleDownload)
	s.mux.HandleFunc("/jmap/upload/", s.handleUpload)
}

func (s *Server) ListenAndServe(addr string) error {
	s.srv = &http.Server{
		Addr:    addr,
		Handler: s.withMiddleware(s.mux),
	}
	// If a custom listener was pre-set via SetListener (admin
	// runtime telemetry path), use it instead of binding again.
	if s.customListener != nil {
		return s.srv.Serve(s.customListener)
	}
	return s.srv.ListenAndServe()
}

func (s *Server) Stop() {
	close(s.done)
	if s.srv != nil {
		s.srv.Close()
	}
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		corsAllowed := s.applyCORSHeaders(w, r)

		if r.Method == "OPTIONS" {
			if r.Header.Get("Origin") != "" && !corsAllowed {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)

		if s.Observability != nil && r.URL.Path == "/jmap/api" {
			s.Observability.EventHistory.Record(observability.EventJMAPRequest, map[string]string{
				"method": r.Method, "path": r.URL.Path,
				"duration_ms": fmt.Sprintf("%d", time.Since(start).Milliseconds()),
			})
		}
	})
}

func sanitizeAllowedOrigins(origins []string) []string {
	clean := make([]string, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" || origin == "*" {
			continue
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		clean = append(clean, origin)
	}
	return clean
}

func (s *Server) applyCORSHeaders(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range s.AllowedOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			return true
		}
	}
	return false
}

// ── API Dispatcher ───────────────────────────────────────────

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		s.writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username, mailboxID, ok := s.authenticate(r)
	if !ok {
		s.writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		s.recordError("read body")
		s.writeError(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		s.recordError("parse request")
		s.writeJMAPError(w, &MethodResponse{Name: "error", Params: map[string]string{"type": "invalidArguments"}, ID: "0"})
		return
	}

	accountID := fmt.Sprintf("%d", mailboxID)
	resp := &Response{SessionState: fmt.Sprintf("%d", time.Now().Unix())}

	for _, raw := range req.MethodCalls {
		mc, err := parseMethodCall(raw)
		if err != nil {
			resp.MethodResponses = append(resp.MethodResponses, MethodResponse{
				Name: "error", ID: "unknown",
				Params: ErrorResponse{Type: "invalidArguments", Detail: "malformed method call"},
			})
			continue
		}

		mr := s.handleMethodCall(r.Context(), mc, username, mailboxID, accountID)
		resp.MethodResponses = append(resp.MethodResponses, *mr)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

func parseMethodCall(raw json.RawMessage) (*MethodCall, error) {
	var obj struct {
		Name   string          `json:"name"`
		Params json.RawMessage `json:"params"`
		ID     string          `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Name != "" {
		return &MethodCall{Name: obj.Name, Params: obj.Params, ID: obj.ID}, nil
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil || len(arr) < 2 {
		return nil, fmt.Errorf("invalid method call format")
	}

	var name string
	json.Unmarshal(arr[0], &name)

	var id string
	if len(arr) >= 3 {
		json.Unmarshal(arr[2], &id)
	}

	return &MethodCall{Name: name, Params: arr[1], ID: id}, nil
}

func (s *Server) handleMethodCall(ctx context.Context, mc *MethodCall, username string, mailboxID uint, accountID string) *MethodResponse {
	switch mc.Name {
	case "Mailbox/get":
		return s.handleMailboxGet(ctx, mc, mailboxID, accountID)
	case "Mailbox/query":
		return s.handleMailboxQuery(ctx, mc, mailboxID, accountID)
	case "Mailbox/changes":
		return s.handleMailboxChanges(ctx, mc, mailboxID, accountID)
	case "Email/get":
		return s.handleEmailGet(ctx, mc, mailboxID, accountID)
	case "Email/query":
		return s.handleEmailQuery(ctx, mc, mailboxID, accountID)
	case "Email/changes":
		return s.handleEmailChanges(ctx, mc, mailboxID, accountID)
	case "Email/set":
		return s.handleEmailSet(ctx, mc, mailboxID, accountID, username)
	case "Submission/set":
		return s.handleSubmissionSet(ctx, mc, mailboxID, accountID, username)
	default:
		if s.Observability != nil {
			s.Observability.Metrics.IncJMAPMethodFailure()
		}
		return &MethodResponse{
			Name: "error", ID: mc.ID,
			Params: ErrorResponse{Type: "unknownMethod", Detail: fmt.Sprintf("unknown method: %s", mc.Name)},
		}
	}
}
