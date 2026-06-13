package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/inspector"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

const certParseTimeout = 10 * time.Second

const maxCertFileSize = 5 * 1024 * 1024 // 5MB

type InspectHandler struct {
	cfg *config.Config
}

func NewInspectHandler(cfg *config.Config) *InspectHandler {
	return &InspectHandler{cfg: cfg}
}

func (h *InspectHandler) Certificate(w http.ResponseWriter, r *http.Request) {
	if err := parseMultipartFormLimited(w, r, maxCertFileSize); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing file")
		return
	}
	defer file.Close()

	if header.Size > maxCertFileSize {
		response.Error(w, http.StatusBadRequest, response.CodeFileTooLarge, "certificate file exceeds 5MB limit")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, maxCertFileSize+1))
	if err != nil {
		slog.Error("failed to read certificate file", "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalError, "failed to read file")
		return
	}

	password := r.FormValue("password")
	if len(password) > 1024 {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "password too long")
		return
	}

	result, err := parseCertWithTimeout(r.Context(), data, header.Filename, password)
	if err != nil {
		slog.Info("certificate parse failed", "error", err)
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "failed to parse certificate")
		return
	}

	slog.Info("certificate inspected",
		"format", result.Format,
		"certCount", result.CertCount,
	)

	response.JSON(w, http.StatusOK, result)
}

const maxCertTextSize = 64 * 1024 // 64KB

func (h *InspectHandler) CertificateText(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PEM string `json:"pem"`
	}

	body := http.MaxBytesReader(w, r.Body, int64(maxCertTextSize))
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "invalid request body")
		return
	}

	if len(req.PEM) == 0 {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing pem field")
		return
	}

	result, err := parseCertWithTimeout(r.Context(), []byte(req.PEM), "paste.pem", "")
	if err != nil {
		slog.Info("certificate text parse failed", "error", err)
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "failed to parse certificate")
		return
	}

	slog.Info("certificate text inspected",
		"format", result.Format,
		"certCount", result.CertCount,
	)

	response.JSON(w, http.StatusOK, result)
}

func parseCertWithTimeout(parent context.Context, data []byte, filename, password string) (*inspector.CertificateInfo, error) {
	ctx, cancel := context.WithTimeout(parent, certParseTimeout)
	defer cancel()

	type result struct {
		info *inspector.CertificateInfo
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		info, err := inspector.ParseCertificate(data, filename, password)
		ch <- result{info, err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("certificate parsing timed out")
	case r := <-ch:
		return r.info, r.err
	}
}
