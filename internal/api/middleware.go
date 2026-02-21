package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

type RateLimiter struct {
	cfg     *config.Config
	rpm     int
	rph     int
	clients map[string]*clientRate
	mu      sync.RWMutex
}

type clientRate struct {
	minuteCount int
	hourCount   int
	minuteReset time.Time
	hourReset   time.Time
}

func NewRateLimiter(ctx context.Context, cfg *config.Config, rpm, rph int) *RateLimiter {
	rl := &RateLimiter{
		cfg:     cfg,
		rpm:     rpm,
		rph:     rph,
		clients: make(map[string]*clientRate),
	}

	go rl.cleanup(ctx)

	return rl
}

func (rl *RateLimiter) cleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, client := range rl.clients {
				if now.After(client.hourReset) {
					delete(rl.clients, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	client, exists := rl.clients[ip]
	if !exists {
		client = &clientRate{
			minuteReset: now.Add(time.Minute),
			hourReset:   now.Add(time.Hour),
		}
		rl.clients[ip] = client
	}

	if now.After(client.minuteReset) {
		client.minuteCount = 0
		client.minuteReset = now.Add(time.Minute)
	}

	if now.After(client.hourReset) {
		client.hourCount = 0
		client.hourReset = now.Add(time.Hour)
	}

	if client.minuteCount >= rl.rpm || client.hourCount >= rl.rph {
		return false
	}

	client.minuteCount++
	client.hourCount++
	return true
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)

		if rl.cfg.TrustedIPs[remoteIP(r)] {
			next.ServeHTTP(w, r)
			return
		}

		if !rl.Allow(ip) {
			w.Header().Set("Retry-After", "60")
			response.Error(w, http.StatusTooManyRequests, response.CodeRateLimited, "rate limit exceeded, please try again later")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// remoteIP returns the direct TCP peer address (no header trust).
// Used for trusted-IP checks where spoofing must be impossible.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// getClientIP returns the end-user IP for rate-limiting.
// Trusts CF-Connecting-IP (set by Cloudflare, not spoofable by clients)
// and falls back to the TCP peer address.
func getClientIP(r *http.Request) string {
	if cfIP := r.Header.Get("CF-Connecting-IP"); cfIP != "" {
		return strings.TrimSpace(cfIP)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func CORSWithConfig(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := cfg.CORSOrigin

			// If no CORS origin configured, don't set any CORS headers
			// (rejects all cross-origin requests).
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if origin != "*" {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, X-Turnstile-Token")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, Content-Length")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(w, r)
	})
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration", time.Since(start),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
