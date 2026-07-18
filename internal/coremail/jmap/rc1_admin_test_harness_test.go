package jmap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type rc1AdminHarness struct {
	auth     func(ctx context.Context, username, password string) bool
	mu       sync.Mutex
	sessions map[string]bool
}

func newRC1AdminHarness(auth func(ctx context.Context, username, password string) bool) *rc1AdminHarness {
	return &rc1AdminHarness{auth: auth, sessions: make(map[string]bool)}
}

func (h *rc1AdminHarness) generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *rc1AdminHarness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "POST" && r.URL.Path == "/admin/login":
		h.handleLogin(w, r)
	case r.Method == "GET" && r.URL.Path == "/admin/queue/summary":
		h.handleQueueSummary(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *rc1AdminHarness) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}

	if !h.auth(r.Context(), req.Username, req.Password) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	sid := h.generateSessionID()
	h.mu.Lock()
	h.sessions[sid] = true
	h.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    sid,
		HttpOnly: true,
		Secure:   false,
		Path:     "/",
	})
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *rc1AdminHarness) handleQueueSummary(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("admin_session")
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.mu.Lock()
	valid := h.sessions[cookie.Value]
	h.mu.Unlock()
	if !valid {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":     0,
		"pending":   0,
		"delivered": 0,
		"failed":    0,
	})
}

func startRC1Admin(t *testing.T, env *rc1IntegratedEnv) string {
	t.Helper()

	authFn := func(ctx context.Context, username, password string) bool {
		_, err := env.eng.Auth.AuthenticateMailbox(ctx, username, password)
		return err == nil
	}

	harness := newRC1AdminHarness(authFn)
	mux := http.NewServeMux()
	mux.Handle("/admin/", harness)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	addr := strings.TrimPrefix(ts.URL, "http://")
	t.Logf("rc1 admin harness listening on %s", addr)
	return addr
}
