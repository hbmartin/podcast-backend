package push

import (
	"context"
	"testing"

	"github.com/hbmartin/podcast-backend/db"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
)

type storeMock struct {
	db.Store
	targets  []db.GetPushTargetsForPodcastRow
	podcast  db.Podcast
	episodes map[string]db.Episode
	cleared  []string
}

func (m *storeMock) GetPushTargetsForPodcast(ctx context.Context, podcastUuid string) ([]db.GetPushTargetsForPodcastRow, error) {
	return m.targets, nil
}

func (m *storeMock) GetPodcastByUUID(ctx context.Context, uuid string) (db.Podcast, error) {
	return m.podcast, nil
}

func (m *storeMock) GetEpisodeByUUID(ctx context.Context, uuid string) (db.Episode, error) {
	if episode, ok := m.episodes[uuid]; ok {
		return episode, nil
	}
	return db.Episode{}, pgx.ErrNoRows
}

func (m *storeMock) ClearPushToken(ctx context.Context, token string) error {
	m.cleared = append(m.cleared, token)
	return nil
}

type sentPush struct {
	token string
	n     Notification
}

type senderMock struct {
	sent []sentPush
	fail map[string]error
}

func (s *senderMock) Send(ctx context.Context, token string, n Notification) error {
	if err, ok := s.fail[token]; ok {
		return err
	}
	s.sent = append(s.sent, sentPush{token: token, n: n})
	return nil
}

func newStoreMock() *storeMock {
	return &storeMock{
		podcast: db.Podcast{Uuid: "pod-1", Title: "Test Show"},
		episodes: map[string]db.Episode{
			"ep-1": {Uuid: "ep-1", Title: "One"},
			"ep-2": {Uuid: "ep-2", Title: "Two"},
			"ep-3": {Uuid: "ep-3", Title: "Three"},
			"ep-4": {Uuid: "ep-4", Title: "Four"},
		},
		targets: []db.GetPushTargetsForPodcastRow{
			{UserID: 1, DeviceID: "d1", PushToken: "TOKEN1"},
			{UserID: 2, DeviceID: "d2", PushToken: "TOKEN2"},
		},
	}
}

func TestNotifierFansOut(t *testing.T) {
	store := newStoreMock()
	sender := &senderMock{}
	n := &Notifier{DB: store, Sender: sender}

	n.NotifyNewEpisodes(context.Background(), "pod-1", []string{"ep-1", "ep-2"})

	assert.Len(t, sender.sent, 4, "2 episodes x 2 devices")
	assert.Equal(t, sentPush{token: "TOKEN1", n: Notification{Title: "Test Show", Body: "One"}}, sender.sent[0])
	assert.Equal(t, sentPush{token: "TOKEN2", n: Notification{Title: "Test Show", Body: "One"}}, sender.sent[1])
	assert.Equal(t, "Two", sender.sent[2].n.Body)
	assert.Empty(t, store.cleared)
}

func TestNotifierCapsEpisodes(t *testing.T) {
	store := newStoreMock()
	store.targets = store.targets[:1]
	sender := &senderMock{}
	n := &Notifier{DB: store, Sender: sender}

	n.NotifyNewEpisodes(context.Background(), "pod-1", []string{"ep-1", "ep-2", "ep-3", "ep-4"})

	assert.Len(t, sender.sent, maxEpisodesPerNotify, "backfill storm capped")
}

func TestNotifierClearsDeadTokens(t *testing.T) {
	store := newStoreMock()
	sender := &senderMock{fail: map[string]error{"TOKEN1": ErrUnregistered}}
	n := &Notifier{DB: store, Sender: sender}

	n.NotifyNewEpisodes(context.Background(), "pod-1", []string{"ep-1", "ep-2"})

	assert.Equal(t, []string{"TOKEN1"}, store.cleared, "dead token cleared once")
	assert.Len(t, sender.sent, 2, "TOKEN2 still receives both episodes")
	for _, sent := range sender.sent {
		assert.Equal(t, "TOKEN2", sent.token)
	}
}

func TestNotifierSkipsUnknownEpisodes(t *testing.T) {
	store := newStoreMock()
	sender := &senderMock{}
	n := &Notifier{DB: store, Sender: sender}

	n.NotifyNewEpisodes(context.Background(), "pod-1", []string{"missing", "ep-1"})

	assert.Len(t, sender.sent, 2, "only the known episode is delivered")
	assert.Equal(t, "One", sender.sent[0].n.Body)
}

func TestNotifierNoTargetsNoWork(t *testing.T) {
	store := newStoreMock()
	store.targets = nil
	sender := &senderMock{}
	n := &Notifier{DB: store, Sender: sender}

	n.NotifyNewEpisodes(context.Background(), "pod-1", []string{"ep-1"})

	assert.Empty(t, sender.sent)
}
