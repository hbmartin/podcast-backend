package handlers

import (
	"context"
	"errors"
	"github.com/hbmartin/podcast-backend/models"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthSuccess(t *testing.T) {
	db := &QuerierMock{PingDbResult: 1}
	r := setup(db)

	code, result, _, err := makeRequest[models.HealthResult](r, "GET", "/health", nil)

	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, result.Healthy)
	assert.True(t, result.Dependencies[0].Healthy)
}

func TestHealthBadDB(t *testing.T) {
	db := &QuerierMock{PingDbError: errors.New("Bad DB")}
	r := setup(db)

	code, result, _, err := makeRequest[models.HealthResult](r, "GET", "/health", nil)

	assert.Nil(t, err)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.False(t, result.Healthy)
	assert.False(t, result.Dependencies[0].Healthy)
}

func TestHealthQueueDependency(t *testing.T) {
	db := &QuerierMock{PingDbResult: 1}

	// healthy queue
	h := Handlers{Queries: db, QueuePing: func(ctx context.Context) error { return nil }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.GetHealth)
	code, result, _, err := makeRequest[models.HealthResult](mux, "GET", "/health", nil)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, result.Healthy)
	assert.Len(t, result.Dependencies, 2)
	assert.Equal(t, "Queue", result.Dependencies[1].Name)
	assert.True(t, result.Dependencies[1].Healthy)

	// unreachable queue Redis flips overall health
	h = Handlers{Queries: db, QueuePing: func(ctx context.Context) error { return errors.New("redis down") }}
	mux = http.NewServeMux()
	mux.HandleFunc("GET /health", h.GetHealth)
	code, result, _, err = makeRequest[models.HealthResult](mux, "GET", "/health", nil)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.False(t, result.Healthy)
	assert.True(t, result.Dependencies[0].Healthy, "DB itself is fine")
	assert.False(t, result.Dependencies[1].Healthy)
	assert.Equal(t, "redis down", result.Dependencies[1].Error)
}
