package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PostStatsSummary handles POST /user/stats/summary: listening time totals
// for one device, or account-wide when device_id is empty. times_started_at
// values are epoch seconds, as reported by the client's stats sync.
func (h Handlers) PostStatsSummary(w http.ResponseWriter, r *http.Request) {
	req := &pb.StatsRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if req.DeviceId != "" {
		device, err := h.Queries.GetDevice(r.Context(), db.GetDeviceParams{UserID: user.ID, DeviceID: req.DeviceId})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeError(w, r, err)
			return
		}

		writeProto(w, http.StatusOK, &pb.StatsResponse{
			TimeSilenceRemoval: device.TimeSilenceRemoval,
			TimeSkipping:       device.TimeSkipping,
			TimeIntroSkipping:  device.TimeIntroSkipping,
			TimeVariableSpeed:  device.TimeVariableSpeed,
			TimeListened:       device.TimeListened,
			TimesStartedAt:     statsStartedAt(device.TimesStartedAt, device.CreatedAt, user.CreatedAt),
		})
		return
	}

	totals, err := h.Queries.GetUserStatsTotals(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.StatsResponse{
		TimeSilenceRemoval: totals.TimeSilenceRemoval,
		TimeSkipping:       totals.TimeSkipping,
		TimeIntroSkipping:  totals.TimeIntroSkipping,
		TimeVariableSpeed:  totals.TimeVariableSpeed,
		TimeListened:       totals.TimeListened,
		TimesStartedAt:     statsStartedAt(totals.EarliestStartedAt, totals.EarliestCreatedAt, user.CreatedAt),
	})
}

// statsStartedAt picks the stats anchor: the device-reported epoch seconds
// when present, else the earliest device row, else the account creation.
func statsStartedAt(reportedSeconds int64, deviceCreated time.Time, userCreated time.Time) *timestamppb.Timestamp {
	if reportedSeconds > 0 {
		return timestamppb.New(time.Unix(reportedSeconds, 0).UTC())
	}
	if !deviceCreated.IsZero() {
		return timestamppb.New(deviceCreated)
	}
	return timestamppb.New(userCreated)
}
