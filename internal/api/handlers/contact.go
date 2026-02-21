package handlers

import (
	"net/http"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

type ContactHandler struct {
	cfg *config.Config
}

func NewContactHandler(cfg *config.Config) *ContactHandler {
	return &ContactHandler{cfg: cfg}
}

func (h *ContactHandler) Email(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ContactEmail == "" {
		response.Error(w, http.StatusNotFound, response.CodeValidationError, "contact email not configured")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{
		"email": h.cfg.ContactEmail,
	})
}
