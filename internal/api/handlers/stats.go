package handlers

import (
	"net/http"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
	"github.com/perspectiveanalytics/filemagic-backend/internal/stats"
)

type StatsHandler struct {
	store *stats.Store
}

func NewStatsHandler(store *stats.Store) *StatsHandler {
	return &StatsHandler{store: store}
}

func (h *StatsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.store.Enabled() {
		response.JSON(w, http.StatusOK, stats.Stats{})
		return
	}
	response.JSON(w, http.StatusOK, h.store.Get())
}

func (h *StatsHandler) Thanks(w http.ResponseWriter, r *http.Request) {
	if !h.store.Enabled() {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	h.store.IncrementThanks()
	response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
