package jmap

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/orvix/orvix/internal/observability"
)

func (s *Server) authenticate(r *http.Request) (string, uint, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", 0, false
	}

	var username, password string

	if strings.HasPrefix(authHeader, "Basic ") {
		payload := strings.TrimPrefix(authHeader, "Basic ")
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", 0, false
		}
		pair := strings.SplitN(string(decoded), ":", 2)
		if len(pair) != 2 {
			return "", 0, false
		}
		username = pair[0]
		password = pair[1]
	} else {
		return "", 0, false
	}

	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx >= 0 {
		ip = ip[:idx]
	}
	ua := r.Header.Get("User-Agent")

	mbox, err := s.Engine.Auth.AuthenticateMailbox(r.Context(), username, password)
	if err != nil || mbox == nil {
		if s.Observability != nil {
			s.Observability.Metrics.IncJMAPAuthFailure()
			s.Observability.EventHistory.Record(observability.EventJMAPAuthFailure, map[string]string{
				"username": username,
			})
		}
		// Record failed login if mailbox can be found by email.
		if mb, lookupErr := s.Engine.Mailboxes.GetByEmail(r.Context(), username, nil); lookupErr == nil && mb != nil {
			s.recordLoginActivity(r.Context(), mb.ID, false, ip, ua)
		}
		return "", 0, false
	}

	if !mbox.AllowJMAP {
		return "", 0, false
	}

	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPAuthSuccess()
		s.Observability.EventHistory.Record(observability.EventJMAPAuthSuccess, map[string]string{
			"username": username,
		})
	}

	s.recordSession(r.Context(), mbox.ID, ip, ua)
	s.recordLoginActivity(r.Context(), mbox.ID, true, ip, ua)

	return username, mbox.ID, true
}

func (s *Server) recordSession(ctx context.Context, mailboxID uint, ip, userAgent string) {
	if s.RecordSession == nil {
		return
	}
	defer func() { recover() }()
	_ = s.RecordSession(ctx, mailboxID, ip, userAgent)
}

func (s *Server) recordLoginActivity(ctx context.Context, mailboxID uint, success bool, ip, userAgent string) {
	if s.RecordLoginActivity == nil {
		return
	}
	defer func() { recover() }()
	_ = s.RecordLoginActivity(ctx, mailboxID, success, ip, userAgent)
}
