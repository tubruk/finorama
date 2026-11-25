package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chickenzord/finorama/internal/vault"
)

type Middleware struct {
	vault      *vault.Vault
	tokens     map[string]time.Time
	tokenMu    sync.RWMutex
	rateLimit  map[string]*rateBucket
	rateMu     sync.Mutex
	allowedOrigins []string
}

type rateBucket struct {
	tokens    float64
	lastCheck time.Time
}

func NewMiddleware(v *vault.Vault, allowedOrigins []string) *Middleware {
	return &Middleware{
		vault:          v,
		tokens:         make(map[string]time.Time),
		rateLimit:      make(map[string]*rateBucket),
		allowedOrigins: allowedOrigins,
	}
}

// LoggingMiddleware logs every HTTP request with status, duration, and client IP.
func (m *Middleware) Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		slog.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration", duration.String(),
			"client_ip", r.RemoteAddr,
		)
	})
}

// CORSMiddleware manages cross-origin access with strict allowlist.
func (m *Middleware) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowOrigin := ""

		if origin != "" {
			for _, allowed := range m.allowedOrigins {
				if allowed == "*" || strings.EqualFold(origin, allowed) {
					allowOrigin = origin
					break
				}
			}
		}

		if allowOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Finorama-Token")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RateLimit limits requests per client IP to prevent brute-forcing.
func (m *Middleware) RateLimit(capacity float64, refillPerSec float64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if idx := strings.LastIndex(ip, ":"); idx != -1 {
				ip = ip[:idx]
			}

			m.rateMu.Lock()
			now := time.Now()

			// Evict stale buckets to prevent unbounded memory growth
			if len(m.rateLimit) > 1000 {
				for k, b := range m.rateLimit {
					if now.Sub(b.lastCheck) > 10*time.Minute {
						delete(m.rateLimit, k)
					}
				}
			}

			bucket, exists := m.rateLimit[ip]
			if !exists {
				bucket = &rateBucket{tokens: capacity, lastCheck: now}
				m.rateLimit[ip] = bucket
			} else {
				elapsed := now.Sub(bucket.lastCheck).Seconds()
				bucket.tokens += elapsed * refillPerSec
				if bucket.tokens > capacity {
					bucket.tokens = capacity
				}
				bucket.lastCheck = now
			}

			if bucket.tokens < 1.0 {
				m.rateMu.Unlock()
				slog.Warn("Rate limit exceeded", "client_ip", ip, "path", r.URL.Path)
				WriteError(w, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "Too many requests. Please slow down.")
				return
			}

			bucket.tokens -= 1.0
			m.rateMu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}

// RequireUnlocked ensures request is authenticated and the vault is unlocked in RAM.
func (m *Middleware) RequireUnlocked(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "Authentication token missing")
			return
		}

		m.tokenMu.RLock()
		expiry, exists := m.tokens[token]
		m.tokenMu.RUnlock()

		if !exists || time.Now().After(expiry) {
			WriteError(w, http.StatusUnauthorized, "SESSION_EXPIRED", "Session expired or invalid")
			return
		}

		if !m.vault.IsUnlocked() {
			WriteError(w, http.StatusLocked, "VAULT_LOCKED", "Vault is locked. Unlock master passphrase first.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) CreateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := base64.URLEncoding.EncodeToString(b)

	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	m.tokens[token] = time.Now().Add(24 * time.Hour)
	return token, nil
}

func (m *Middleware) RevokeAllTokens() {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	m.tokens = make(map[string]time.Time)
}

func extractToken(r *http.Request) string {
	if header := r.Header.Get("X-Finorama-Token"); header != "" {
		return strings.TrimSpace(header)
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	cookie, err := r.Cookie("finorama_session")
	if err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return ""
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
