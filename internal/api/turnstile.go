package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

const maxTurnstileResponseSize = 16 * 1024 // 16 KB — Cloudflare responses are tiny

type turnstileResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

func TurnstileMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	client := &http.Client{Timeout: 10 * time.Second}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.TurnstileSecret == "" {
				next.ServeHTTP(w, r)
				return
			}

			if cfg.TrustedIPs[remoteIP(r)] {
				next.ServeHTTP(w, r)
				return
			}

			token := r.Header.Get("X-Turnstile-Token")
			if token == "" {
				response.Error(w, http.StatusForbidden, response.CodeValidationError, "missing turnstile token")
				return
			}

			ip := getClientIP(r)

			resp, err := client.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
				"secret":   {cfg.TurnstileSecret},
				"response": {token},
				"remoteip": {ip},
			})
			if err != nil {
				slog.Error("turnstile verification request failed", "error", err)
				response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "security verification failed")
				return
			}
			defer resp.Body.Close()

			var result turnstileResponse
			if err := json.NewDecoder(io.LimitReader(resp.Body, maxTurnstileResponseSize)).Decode(&result); err != nil {
				slog.Error("turnstile response decode failed", "error", err)
				response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "security verification failed")
				return
			}

			if !result.Success {
				slog.Warn("turnstile verification failed", "ip", ip, "errors", result.ErrorCodes)
				response.Error(w, http.StatusForbidden, response.CodeValidationError, "security verification failed")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
