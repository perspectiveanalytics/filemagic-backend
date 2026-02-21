package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

// userFacingError returns user-friendly messages for known safe errors,
// and a generic message for internal/unexpected errors.
func userFacingError(err string) string {
	safeMessages := []string{
		"no images found in PDF",
		"no audio stream found",
		"password incorrect",
		"invalid password",
		"file is encrypted",
		"timed out",
		"output file too large",
		"generated gif is too large",
		"too many files",
		"decompressed size exceeds",
	}
	lower := strings.ToLower(err)
	for _, msg := range safeMessages {
		if strings.Contains(lower, strings.ToLower(msg)) {
			return err
		}
	}
	return "conversion failed, please try again"
}

type QueueHandler struct {
	queue *queue.Queue
}

func NewQueueHandler(q *queue.Queue) *QueueHandler {
	return &QueueHandler{queue: q}
}

func (h *QueueHandler) Status(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, map[string]int{
		"queueLength": h.queue.Length(),
	})
}

func (h *QueueHandler) Position(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		response.Error(w, http.StatusBadRequest, response.CodeValidationError, "missing job ID")
		return
	}

	job, found := h.queue.GetJob(jobID)
	if !found {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "job not found")
		return
	}

	resp := response.StatusResponse{
		JobID:    job.ID,
		Status:   string(job.Status),
		Position: h.queue.GetPosition(job.ID),
	}

	if job.Status == queue.StatusDone {
		resp.DownloadURL = fmt.Sprintf("/api/download/%s", job.ID)
		resp.InputSize = job.InputSize
		resp.OutputSize = job.OutputSize
		if job.Metadata != nil {
			resp.Metadata = job.Metadata
		}
	}

	if job.Status == queue.StatusError {
		slog.Warn("job failed", "jobId", job.ID, "error", job.Error)
		resp.Error = userFacingError(job.Error)
	}

	response.JSON(w, http.StatusOK, resp)
}
