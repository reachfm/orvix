package jmap

import (
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

	mbox, err := s.Engine.Auth.AuthenticateMailbox(r.Context(), username, password)
	if err != nil || mbox == nil {
		if s.Observability != nil {
			s.Observability.Metrics.IncJMAPAuthFailure()
			s.Observability.EventHistory.Record(observability.EventJMAPAuthFailure, map[string]string{
				"username": username,
			})
		}
		return "", 0, false
	}

	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPAuthSuccess()
		s.Observability.EventHistory.Record(observability.EventJMAPAuthSuccess, map[string]string{
			"username": username,
		})
	}

	return username, mbox.ID, true
}
