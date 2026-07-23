package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
)

// feedbackMock records InsertFeedback calls on top of QuerierMock.
type feedbackMock struct {
	QuerierMock

	inserted []db.InsertFeedbackParams
}

func newFeedbackMock() *feedbackMock {
	m := &feedbackMock{}
	m.GetUserByUUIDResult = db.User{ID: 42, Uuid: testUserUUID, Email: "mail@test.com", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	return m
}

func (m *feedbackMock) InsertFeedback(ctx context.Context, arg db.InsertFeedbackParams) error {
	m.inserted = append(m.inserted, arg)
	return nil
}

func feedbackRouter(m *feedbackMock) *http.ServeMux {
	handlers := Handlers{Queries: m, Config: testAuthConfig}
	mux := http.NewServeMux()
	mux.Handle("POST /support/feedback", mockAuthMiddleware(http.HandlerFunc(handlers.PostSupportFeedback)))
	mux.HandleFunc("POST /anonymous/feedback", handlers.PostAnonymousFeedback)
	return mux
}

func TestSupportFeedbackStoresReportWithUser(t *testing.T) {
	m := newFeedbackMock()
	router := feedbackRouter(m)

	code, _, err := makeProtoRequest(router, "/support/feedback", &pb.SupportFeedbackRequest{
		Message:           "The player froze after seeking",
		Subject:           "Shake report",
		Inbox:             "beta",
		Logs:              "line one\nline two",
		BitdriftSessionId: "session-123",
		DeviceInfo:        "iPhone17,1 iOS 26.5",
		AppVersion:        "7.99 (1234)",
	}, nil)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	if assert.Len(t, m.inserted, 1) {
		report := m.inserted[0]
		if assert.NotNil(t, report.UserID) {
			assert.Equal(t, int64(42), *report.UserID)
		}
		assert.Equal(t, "The player froze after seeking", report.Message)
		assert.Equal(t, "line one\nline two", report.Logs)
		assert.Equal(t, "session-123", report.BitdriftSessionID)
		assert.Equal(t, "iPhone17,1 iOS 26.5", report.DeviceInfo)
		assert.Equal(t, "7.99 (1234)", report.AppVersion)
	}
}

func TestAnonymousFeedbackStoresReportWithoutUser(t *testing.T) {
	m := newFeedbackMock()
	router := feedbackRouter(m)

	code, _, err := makeProtoRequest(router, "/anonymous/feedback", &pb.SupportFeedbackRequest{
		Message: "Anonymous report",
	}, nil)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	if assert.Len(t, m.inserted, 1) {
		assert.Nil(t, m.inserted[0].UserID)
	}
}

func TestFeedbackValidationAndTruncation(t *testing.T) {
	m := newFeedbackMock()
	router := feedbackRouter(m)

	// Empty message is rejected before storage.
	code, _, _ := makeProtoRequest(router, "/anonymous/feedback", &pb.SupportFeedbackRequest{}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Empty(t, m.inserted)

	// Oversized fields are stored truncated, not rejected.
	code, _, _ = makeProtoRequest(router, "/anonymous/feedback", &pb.SupportFeedbackRequest{
		Message: strings.Repeat("m", maxFeedbackMessage+50),
		Logs:    strings.Repeat("l", maxFeedbackLogs+50),
	}, nil)
	assert.Equal(t, http.StatusOK, code)
	if assert.Len(t, m.inserted, 1) {
		assert.Len(t, m.inserted[0].Message, maxFeedbackMessage)
		assert.Len(t, m.inserted[0].Logs, maxFeedbackLogs)
	}
}

func TestTruncatePreservesUTF8AtByteBoundary(t *testing.T) {
	tests := []struct {
		name string
		text string
		max  int
		want string
	}{
		{name: "two-byte", text: "abé", max: 3, want: "ab"},
		{name: "three-byte", text: "ab€", max: 4, want: "ab"},
		{name: "four-byte", text: "ab🙂", max: 5, want: "ab"},
		{name: "complete-rune", text: "ab🙂", max: 6, want: "ab🙂"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.text, tt.max)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, len(got), tt.max)
			assert.True(t, utf8.ValidString(got))
		})
	}
}
