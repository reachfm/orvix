package jmap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/orvix/orvix/internal/observability"
)

func (s *Server) writeError(w http.ResponseWriter, msg string, code int) {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPError()
	}
	http.Error(w, fmt.Sprintf(`{"error":"%s"}`, msg), code)
}

func (s *Server) recordError(detail string) {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPError()
		s.Observability.EventHistory.Record(observability.EventJMAPError, map[string]string{"detail": detail})
	}
}

func (s *Server) writeJMAPError(w http.ResponseWriter, mr *MethodResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp := &Response{
		MethodResponses: []MethodResponse{*mr},
		SessionState:    fmt.Sprintf("%d", time.Now().Unix()),
	}
	json.NewEncoder(w).Encode(resp)
}
