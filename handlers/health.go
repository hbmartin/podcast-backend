package handlers

import (
	"github.com/hbmartin/podcast-backend/models"
	"net/http"
)

// GetHealth godoc
//
//	@Summary	Determines if the app is healthy
//	@Schemes
//	@Description	Returns HTTP 200 if the app is healthy and 400 if not
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	models.HealthResult
//	@Failure		400	{object}	models.HealthResult
//	@Router			/health [get]
func (h Handlers) GetHealth(w http.ResponseWriter, r *http.Request) {
	pingResult, err := h.Queries.PingDb(r.Context())
	isDbHealthy := err == nil && pingResult == 1
	dbHealth := models.HealthResultItem{Name: "DB", Healthy: isDbHealthy}
	if err != nil {
		dbHealth.Error = err.Error()
	}

	result := &models.HealthResult{Healthy: isDbHealthy, Dependencies: []models.HealthResultItem{dbHealth}}

	// the queue's Redis is only checked when the queue is configured
	if h.QueuePing != nil {
		queueHealth := models.HealthResultItem{Name: "Queue", Healthy: true}
		if err := h.QueuePing(r.Context()); err != nil {
			queueHealth.Healthy = false
			queueHealth.Error = err.Error()
			result.Healthy = false
		}
		result.Dependencies = append(result.Dependencies, queueHealth)
	}

	status := http.StatusOK
	if !result.Healthy {
		status = http.StatusInternalServerError
	}

	writeJSON(w, status, result)
}

// GetHealthHTML serves GET /health.html, the endpoint the Pocket Casts status
// page probes on the refresh host.
func (h Handlers) GetHealthHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
