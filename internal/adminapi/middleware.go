//go:build legacy_adminapi

package adminapi

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"time"
)

// RequireSession validates that the request has a valid session.
func (s *Server) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := s.getSession(r)
		if session == nil {
			jsonError(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		if requiresCSRF(r.Method) && !s.validateCSRF(w, r, session) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePermission checks that the authenticated session has a specific permission.
func (s *Server) RequirePermission(perm Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session := s.getSession(r)
			if session == nil {
				jsonError(w, "not authenticated", http.StatusUnauthorized)
				return
			}
			if !session.HasPermission(perm) {
				s.recordAudit(AuditPermissionDenied, session.Username, string(session.Role), r, "missing_"+string(perm))
				jsonError(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireCSRF validates a double-submit CSRF token for state-changing requests.
func (s *Server) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresCSRF(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		session := s.getSession(r)
		if session == nil {
			jsonError(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		if !s.validateCSRF(w, r, session) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requiresCSRF(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func (s *Server) validateCSRF(w http.ResponseWriter, r *http.Request, session *Session) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		s.recordAudit(AuditPermissionDenied, session.Username, string(session.Role), r, "csrf_cookie_missing")
		jsonError(w, "CSRF token missing", http.StatusForbidden)
		return false
	}
	header := r.Header.Get("X-CSRF-Token")
	if header == "" {
		s.recordAudit(AuditPermissionDenied, session.Username, string(session.Role), r, "csrf_header_missing")
		jsonError(w, "CSRF token missing", http.StatusForbidden)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 ||
		subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(header)) != 1 {
		s.recordAudit(AuditPermissionDenied, session.Username, string(session.Role), r, "csrf_mismatch")
		jsonError(w, "CSRF token mismatch", http.StatusForbidden)
		return false
	}
	return true
}

// RequireRole checks that the authenticated session has one of the specified roles.
func (s *Server) RequireRole(roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session := s.getSession(r)
			if session == nil {
				jsonError(w, "not authenticated", http.StatusUnauthorized)
				return
			}
			allowed := false
			for _, role := range roles {
				if session.Role == role {
					allowed = true
					break
				}
			}
			if !allowed {
				s.recordAudit(AuditPermissionDenied, session.Username, string(session.Role), r, "missing_role")
				jsonError(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AuditMiddleware records a generic audit event for each request.
func (s *Server) AuditMiddleware(action AuditAction) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session := s.getSession(r)
			if session != nil {
				s.recordAudit(action, session.Username, string(session.Role), r, "accessed")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORSMiddleware adds CORS headers for the admin frontend.
func CORSMiddleware(next http.Handler) http.Handler {
	return CORSMiddlewareWithOrigins(next, nil)
}

// CORSMiddlewareWithOrigins adds CORS headers for explicit trusted origins only.
func CORSMiddlewareWithOrigins(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" || origin == "*" {
			continue
		}
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			if origin != "" {
				if _, ok := allowed[origin]; !ok {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs all admin API requests.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[admin] %s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
