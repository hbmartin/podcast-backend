package handlers

import (
	"log/slog"
	"net/http"

	"github.com/hbmartin/podcast-backend/db"
)

// persistPushState records the push registration piggybacked on user/update:
// the device's APNs token, the global toggle, and the positional
// push_messages_on bit-string (one '0'/'1' per entry of the podcasts CSV,
// already the AND of the global and per-podcast toggles at send time, so it
// is authoritative here). Registration must never fail the refresh response,
// so errors are logged and swallowed; anonymous requests are a no-op.
func (h Handlers) persistPushState(r *http.Request, deviceID, pushToken, pushOn, pushMessagesOn string, podcastUuids []string) {
	if pushOn == "" && pushToken == "" {
		return // no push fields: an older or third-party client
	}

	ctxUser := getUser(r.Context())
	if ctxUser == nil || deviceID == "" {
		return
	}

	user, err := h.Queries.GetUserByUUID(r.Context(), ctxUser.UUID)
	if err != nil {
		slog.Warn("push registration: unable to resolve user", "error", err)
		return
	}

	if err := h.Queries.UpsertDevicePush(r.Context(), db.UpsertDevicePushParams{
		UserID:    user.ID,
		DeviceID:  deviceID,
		PushToken: pushToken,
		PushOn:    pushOn == "true",
	}); err != nil {
		slog.Warn("push registration: unable to store device state", "error", err)
		return
	}

	if pushMessagesOn == "" {
		return
	}
	enabled := make([]string, 0, len(podcastUuids))
	for i, uuid := range podcastUuids {
		if i < len(pushMessagesOn) && pushMessagesOn[i] == '1' && uuidPattern.MatchString(uuid) {
			enabled = append(enabled, uuid)
		}
	}
	if err := h.Queries.SetPodcastNotifyFlags(r.Context(), db.SetPodcastNotifyFlagsParams{
		UserID:      user.ID,
		NotifyUuids: enabled,
	}); err != nil {
		slog.Warn("push registration: unable to store per-podcast flags", "error", err)
	}
}
