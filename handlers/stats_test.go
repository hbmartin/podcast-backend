package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
)

type statsMock struct {
	QuerierMock

	devices map[string]db.Device
	totals  db.GetUserStatsTotalsRow
}

func newStatsMock() *statsMock {
	m := &statsMock{devices: map[string]db.Device{}}
	m.GetUserByUUIDResult = db.User{ID: 42, Uuid: testUserUUID, CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	return m
}

func (m *statsMock) GetDevice(ctx context.Context, arg db.GetDeviceParams) (db.Device, error) {
	if device, ok := m.devices[arg.DeviceID]; ok {
		return device, nil
	}
	return db.Device{}, pgx.ErrNoRows
}

func (m *statsMock) GetUserStatsTotals(ctx context.Context, userID int64) (db.GetUserStatsTotalsRow, error) {
	return m.totals, nil
}

func statsRouter(m *statsMock) *http.ServeMux {
	handlers := Handlers{Queries: m, Config: testAuthConfig}
	mux := http.NewServeMux()
	mux.Handle("POST /user/stats/summary", mockAuthMiddleware(http.HandlerFunc(handlers.PostStatsSummary)))
	return mux
}

func TestStatsSummaryPerDevice(t *testing.T) {
	m := newStatsMock()
	m.devices["dev-1"] = db.Device{
		DeviceID: "dev-1", TimeListened: 3600, TimeSkipping: 60,
		TimesStartedAt: 1700000000, // epoch seconds
		CreatedAt:      time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	router := statsRouter(m)

	resp := &pb.StatsResponse{}
	code, _, err := makeProtoRequest(router, "/user/stats/summary",
		&pb.StatsRequest{DeviceId: "dev-1", DeviceType: 1}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(3600), resp.TimeListened)
	assert.Equal(t, int64(60), resp.TimeSkipping)
	assert.Equal(t, int64(1700000000), resp.TimesStartedAt.Seconds)
}

func TestStatsSummaryAccountWide(t *testing.T) {
	m := newStatsMock()
	m.totals = db.GetUserStatsTotalsRow{
		TimeListened: 7200, TimeVariableSpeed: 100,
		EarliestStartedAt: 0, // never reported: fall back to earliest created
		EarliestCreatedAt: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	router := statsRouter(m)

	resp := &pb.StatsResponse{}
	code, _, err := makeProtoRequest(router, "/user/stats/summary",
		&pb.StatsRequest{DeviceId: "", DeviceType: 1}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(7200), resp.TimeListened)
	assert.Equal(t, time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).Unix(), resp.TimesStartedAt.Seconds)
}

func TestStatsSummaryUnknownDevice(t *testing.T) {
	router := statsRouter(newStatsMock())

	code, _, _ := makeProtoRequest(router, "/user/stats/summary",
		&pb.StatsRequest{DeviceId: "nope"}, nil)
	assert.Equal(t, http.StatusNotFound, code)
}
