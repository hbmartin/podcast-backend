package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/hbmartin/podcast-backend/db"

	"github.com/stretchr/testify/assert"
)

// pushMock records push registration writes on top of the refresh catalog
// behavior.
type pushMock struct {
	refreshMock
	devicePush  []db.UpsertDevicePushParams
	notifyFlags []db.SetPodcastNotifyFlagsParams
}

func (m *pushMock) UpsertDevicePush(ctx context.Context, arg db.UpsertDevicePushParams) error {
	m.devicePush = append(m.devicePush, arg)
	return nil
}

func (m *pushMock) SetPodcastNotifyFlags(ctx context.Context, arg db.SetPodcastNotifyFlagsParams) error {
	m.notifyFlags = append(m.notifyFlags, arg)
	return nil
}

func newPushMock() *pushMock {
	m := &pushMock{refreshMock: *newRefreshMock()}
	m.GetUserByUUIDResult = db.User{ID: 42, Uuid: testUserUUID}
	return m
}

func pushRouter(m *pushMock, authenticated bool) *http.ServeMux {
	handlers := Handlers{Queries: m, Config: testAuthConfig}
	mux := http.NewServeMux()
	handler := http.HandlerFunc(handlers.PostRefreshUserUpdate)
	if authenticated {
		mux.Handle("POST /user/update", mockAuthMiddleware(handler))
	} else {
		mux.Handle("POST /user/update", handler)
	}
	return mux
}

const secondPodcastUUID = "1f0e0d0c-0b0a-4988-8776-655443322111"

func TestPushRegistrationPersisted(t *testing.T) {
	m := newPushMock()
	seedCatalog(&m.refreshMock)
	router := pushRouter(m, true)

	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":         testPodcastUUID + "," + secondPodcastUUID,
		"last_episodes":    ",",
		"device":           "device-1",
		"push_token":       "ABCDEF0123456789",
		"push_on":          "true",
		"push_messages_on": "01",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", resp.Status)

	assert.Len(t, m.devicePush, 1)
	assert.Equal(t, int64(42), m.devicePush[0].UserID)
	assert.Equal(t, "device-1", m.devicePush[0].DeviceID)
	assert.Equal(t, "ABCDEF0123456789", m.devicePush[0].PushToken)
	assert.True(t, m.devicePush[0].PushOn)

	assert.Len(t, m.notifyFlags, 1)
	assert.Equal(t, int64(42), m.notifyFlags[0].UserID)
	assert.Equal(t, []string{secondPodcastUUID}, m.notifyFlags[0].NotifyUuids, "only the '1' position is enabled")
}

func TestPushRegistrationGlobalOff(t *testing.T) {
	m := newPushMock()
	seedCatalog(&m.refreshMock)
	router := pushRouter(m, true)

	// no token yet, push disabled: state still recorded, flags wiped
	code, _, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":         testPodcastUUID,
		"last_episodes":    "",
		"device":           "device-1",
		"push_on":          "false",
		"push_messages_on": "0",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, m.devicePush, 1)
	assert.False(t, m.devicePush[0].PushOn)
	assert.Empty(t, m.devicePush[0].PushToken)
	assert.Len(t, m.notifyFlags, 1)
	assert.Empty(t, m.notifyFlags[0].NotifyUuids)
}

func TestPushRegistrationAnonymousIgnored(t *testing.T) {
	m := newPushMock()
	seedCatalog(&m.refreshMock)
	router := pushRouter(m, false)

	code, _, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":         testPodcastUUID,
		"last_episodes":    "",
		"device":           "device-1",
		"push_token":       "ABCDEF0123456789",
		"push_on":          "true",
		"push_messages_on": "1",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code, "refresh still succeeds signed out")
	assert.Empty(t, m.devicePush)
	assert.Empty(t, m.notifyFlags)
}

func TestPushRegistrationAbsentFieldsIgnored(t *testing.T) {
	m := newPushMock()
	seedCatalog(&m.refreshMock)
	router := pushRouter(m, true)

	code, _, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":      testPodcastUUID,
		"last_episodes": "",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Empty(t, m.devicePush, "no push fields, no writes")
	assert.Empty(t, m.notifyFlags)
}

func TestPushRegistrationBitstringMismatchTolerated(t *testing.T) {
	m := newPushMock()
	seedCatalog(&m.refreshMock)
	router := pushRouter(m, true)

	// bit-string shorter than the podcast list must not panic; missing
	// positions are treated as off
	code, _, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":         testPodcastUUID + "," + secondPodcastUUID + ",not-a-uuid",
		"last_episodes":    ",,",
		"device":           "device-1",
		"push_token":       "ABCDEF0123456789",
		"push_on":          "true",
		"push_messages_on": "1",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, m.notifyFlags, 1)
	assert.Equal(t, []string{testPodcastUUID}, m.notifyFlags[0].NotifyUuids)
}
