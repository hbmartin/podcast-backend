package handlers

import (
	"net/http"
	"unicode/utf8"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
)

// Field caps keep one report from bloating the table; the app truncates its
// log tail before sending, so anything larger is misbehaving.
const (
	maxFeedbackMessage = 10_000
	maxFeedbackLogs    = 512 * 1024
	maxFeedbackField   = 1_000
	// MaxFeedbackBody caps the raw feedback protobuf body (docs/AppAttest.md §4).
	MaxFeedbackBody = 1 << 20 // 1 MiB
)

// PostSupportFeedback handles POST /support/feedback: a feedback report from a
// signed-in user (the support flow and shake-to-report in TestFlight builds).
// The client only checks the response status.
func (h Handlers) PostSupportFeedback(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	userID := user.ID
	h.storeFeedback(w, r, &userID)
}

// PostAnonymousFeedback handles POST /anonymous/feedback: the same report
// without an account attached.
func (h Handlers) PostAnonymousFeedback(w http.ResponseWriter, r *http.Request) {
	h.storeFeedback(w, r, nil)
}

func (h Handlers) storeFeedback(w http.ResponseWriter, r *http.Request, userID *int64) {
	req := &pb.SupportFeedbackRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if req.Message == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "message is required")
		return
	}

	err := h.Queries.InsertFeedback(r.Context(), db.InsertFeedbackParams{
		UserID:            userID,
		Message:           truncate(req.Message, maxFeedbackMessage),
		Subject:           truncate(req.Subject, maxFeedbackField),
		Inbox:             truncate(req.Inbox, maxFeedbackField),
		Logs:              truncate(req.Logs, maxFeedbackLogs),
		BitdriftSessionID: truncate(req.BitdriftSessionId, maxFeedbackField),
		DeviceInfo:        truncate(req.DeviceInfo, maxFeedbackField),
		AppVersion:        truncate(req.AppVersion, maxFeedbackField),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// truncate limits a string to max bytes while preserving valid UTF-8 for
// PostgreSQL text columns.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] // nosemgrep: go.byte-slice-in-truncation-helper -- end is rewound to a UTF-8 rune boundary above.
}
