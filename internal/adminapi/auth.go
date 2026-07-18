//go:build legacy_adminapi

package adminapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/observability"
)

const sessionDuration = 24 * time.Hour

const sessionCookieName = "admin_session"
const csrfCookieName = "admin_csrf"

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		jsonError(w, "username and password required", http.StatusBadRequest)
		return
	}

	mbox, err := s.Engine.Auth.AuthenticateMailbox(r.Context(), req.Username, req.Password)
	if err != nil || mbox == nil {
		s.recordAudit(AuditLoginFailure, req.Username, "", r, "invalid_credentials")
		jsonError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	role := RoleAdmin

	token, err := generateToken()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrfToken, err := generateToken()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	session := &Session{
		Token:     token,
		CSRFToken: csrfToken,
		UserID:    mbox.ID,
		Username:  req.Username,
		Role:      role,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionDuration),
	}
	s.Sessions.Set(session)

	// Set HttpOnly cookie — not accessible via JavaScript.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
		MaxAge:   int(sessionDuration.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/admin",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
		MaxAge:   int(sessionDuration.Seconds()),
	})

	s.recordAudit(AuditLoginSuccess, req.Username, string(role), r, "success")

	jsonOK(w, LoginResponse{
		UserID:      mbox.ID,
		Username:    req.Username,
		Role:        role,
		Permissions: GetPermissions(role),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := extractToken(r)
	if token != "" {
		session := s.Sessions.Get(token)
		if session != nil {
			s.recordAudit(AuditLogout, session.Username, string(session.Role), r, "success")
		}
		s.Sessions.Delete(token)
	}

	// Clear the cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/admin",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	jsonOK(w, map[string]string{"status": "logged_out"})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	session := s.getSession(r)
	if session == nil {
		jsonError(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	jsonOK(w, SessionResponse{
		UserID:      session.UserID,
		Username:    session.Username,
		Role:        session.Role,
		Permissions: session.Permissions(),
	})
}

func (s *Server) getSession(r *http.Request) *Session {
	token := extractToken(r)
	if token == "" {
		return nil
	}
	session := s.Sessions.Get(token)
	if session == nil || session.IsExpired() {
		if session != nil {
			s.Sessions.Delete(token)
			s.recordAudit(AuditSessionExpired, session.Username, string(session.Role), r, "expired")
		}
		return nil
	}
	return session
}

func extractToken(r *http.Request) string {
	// Check cookie first (primary delivery mechanism).
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
		return cookie.Value
	}
	// Fallback: Authorization header (for API clients).
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) recordAudit(action AuditAction, actor, role string, r *http.Request, result string) {
	ip := r.RemoteAddr
	ua := r.UserAgent()

	if s.Observability != nil && s.Observability.EventHistory != nil {
		s.Observability.EventHistory.Record(observability.EventType("admin_"+string(action)), map[string]string{
			"actor":     actor,
			"role":      role,
			"ip":        ip,
			"userAgent": ua,
			"result":    result,
		})
	}
	if s.AuditStore != nil {
		if err := s.AuditStore.Record(r.Context(), &audit.Entry{
			Actor:     actor,
			Role:      role,
			Action:    string(action),
			Result:    result,
			IP:        ip,
			UserAgent: ua,
		}); err != nil {
			log.Printf("[admin] audit store error: %v", err)
		}
	}

	log.Printf("[admin] audit: action=%s actor=%s role=%s ip=%s result=%s", action, actor, role, ip, result)
}
