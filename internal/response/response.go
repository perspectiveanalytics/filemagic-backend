package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type ErrorCode string

const (
	CodeValidationError  ErrorCode = "VALIDATION_ERROR"
	CodeQueueFull        ErrorCode = "QUEUE_FULL"
	CodeFileTooLarge     ErrorCode = "FILE_TOO_LARGE"
	CodeRateLimited ErrorCode = "RATE_LIMITED"
	CodeNotFound         ErrorCode = "NOT_FOUND"
	CodeInternalError    ErrorCode = "INTERNAL_ERROR"
)

type SubmitResponse struct {
	JobID         string `json:"jobId"`
	Position      int    `json:"position"`
	EstimatedWait int    `json:"estimatedWait"`
}

type StatusResponse struct {
	JobID       string `json:"jobId"`
	Status      string `json:"status"`
	Position    int    `json:"position"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	Error       string `json:"error,omitempty"`
	InputSize   int64          `json:"inputSize,omitempty"`
	OutputSize  int64          `json:"outputSize,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type ErrorResponse struct {
	Error string    `json:"error"`
	Code  ErrorCode `json:"code"`
}

type HealthResponse struct {
	Status      string `json:"status"`
	QueueLength int    `json:"queueLength"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func Error(w http.ResponseWriter, status int, code ErrorCode, message string) {
	if status >= 400 && status < 500 {
		slog.Info("rejected", "code", code, "status", status)
	}
	JSON(w, status, ErrorResponse{
		Error: message,
		Code:  code,
	})
}
