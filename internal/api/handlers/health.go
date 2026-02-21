package handlers

import (
	"net/http"

	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

type HealthHandler struct {
	queue *queue.Queue
}

func NewHealthHandler(q *queue.Queue) *HealthHandler {
	return &HealthHandler{queue: q}
}

func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, response.HealthResponse{
		Status:      "ok",
		QueueLength: h.queue.Length(),
	})
}
