package syncsvc

import (
	"context"
	"testing"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestNextTokenMonotonic(t *testing.T) {
	first := NextToken(0)
	assert.Greater(t, first, int64(0))

	// a previous token in the future must still advance
	future := first + 1_000_000
	assert.Equal(t, future+1, NextToken(future))
}

func podcastRecord(uuid string) *pb.Record {
	return &pb.Record{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
		Uuid:       uuid,
		Subscribed: wrapperspb.Bool(true),
	}}}
}

func episodeRecord(uuid string, playedUpTo int64, playedModified int64) *pb.Record {
	return &pb.Record{Record: &pb.Record_Episode{Episode: &pb.SyncUserEpisode{
		Uuid:               uuid,
		PodcastUuid:        "pod-1",
		PlayedUpTo:         wrapperspb.Int64(playedUpTo),
		PlayedUpToModified: wrapperspb.Int64(playedModified),
	}}}
}

func TestApplyUpdateSubscribeAndEcho(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}

	resp, err := engine.ApplyUpdate(context.Background(), 1, &pb.SyncUpdateRequest{
		LastModified: 0,
		Records:      []*pb.Record{podcastRecord("pod-1")},
	})

	assert.NoError(t, err)
	assert.Greater(t, resp.LastModified, int64(0))
	assert.Equal(t, resp.LastModified, store.user.SyncLastModified)

	// the applied record is echoed back (idempotent import on the client)
	assert.Len(t, resp.Records, 1)
	echoed := resp.Records[0].GetPodcast()
	assert.Equal(t, "pod-1", echoed.Uuid)
	assert.True(t, echoed.Subscribed.GetValue())
	assert.False(t, echoed.IsDeleted.GetValue())
}

func TestApplyUpdateDispatchesUnknownFeedsAfterCommitAndDeduplicates(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	originalHook := OnUnknownPodcast
	t.Cleanup(func() { OnUnknownPodcast = originalHook })

	var gotUserID int64
	var gotFeeds []string
	OnUnknownPodcast = func(userID int64, feedURL string) {
		assert.False(t, store.inTx, "network/queue work must run after commit")
		gotUserID = userID
		gotFeeds = append(gotFeeds, feedURL)
	}
	record := func() *pb.Record {
		return &pb.Record{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
			Uuid:       "unknown-podcast",
			Subscribed: wrapperspb.Bool(true),
			FeedUrl:    wrapperspb.String("https://example.com/feed.xml"),
		}}}
	}

	_, err := engine.ApplyUpdate(context.Background(), 1, &pb.SyncUpdateRequest{
		Records: []*pb.Record{record(), record()},
	})

	assert.NoError(t, err)
	assert.Equal(t, int64(1), gotUserID)
	assert.Equal(t, []string{"https://example.com/feed.xml"}, gotFeeds)

	store.catalogFeeds["https://example.com/feed.xml"] = db.Podcast{Uuid: "catalog-podcast"}
	_, err = engine.ApplyUpdate(context.Background(), 1, &pb.SyncUpdateRequest{Records: []*pb.Record{record()}})
	assert.NoError(t, err)
	assert.Equal(t, []string{"https://example.com/feed.xml"}, gotFeeds,
		"a feed already ingested under its canonical catalog UUID must not be enqueued again")
}

func TestApplyUpdateIncrementalReadback(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	// device A subscribes
	respA, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{podcastRecord("pod-1")}})
	assert.NoError(t, err)

	// device B, empty request from lastModified 0, sees the podcast
	respB, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{LastModified: 0})
	assert.NoError(t, err)
	assert.Len(t, respB.Records, 1)

	// device B again from its new token: nothing new
	respB2, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{LastModified: respB.LastModified})
	assert.NoError(t, err)
	assert.Empty(t, respB2.Records)

	// tokens strictly increase
	assert.Greater(t, respB.LastModified, respA.LastModified)
	assert.Greater(t, respB2.LastModified, respB.LastModified)
}

func TestEpisodePerFieldLastWriterWins(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	// initial state: position 100 at device-time 2000
	_, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{episodeRecord("ep-1", 100, 2000)}})
	assert.NoError(t, err)
	assert.Equal(t, int64(100), store.episodes["ep-1"].PlayedUpTo)

	// stale update (device-time 1000) must not win
	_, err = engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{episodeRecord("ep-1", 50, 1000)}})
	assert.NoError(t, err)
	assert.Equal(t, int64(100), store.episodes["ep-1"].PlayedUpTo)

	// newer update wins
	_, err = engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{episodeRecord("ep-1", 200, 3000)}})
	assert.NoError(t, err)
	assert.Equal(t, int64(200), store.episodes["ep-1"].PlayedUpTo)
	assert.Equal(t, int64(3000), store.episodes["ep-1"].PlayedUpToModified)

	// starring must not clobber the position
	_, err = engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{
		{Record: &pb.Record_Episode{Episode: &pb.SyncUserEpisode{
			Uuid:            "ep-1",
			Starred:         wrapperspb.Bool(true),
			StarredModified: wrapperspb.Int64(4000),
		}}},
	}})
	assert.NoError(t, err)
	assert.Equal(t, int64(200), store.episodes["ep-1"].PlayedUpTo)
	assert.True(t, store.episodes["ep-1"].Starred)
	assert.Equal(t, "pod-1", store.episodes["ep-1"].PodcastUuid, "podcast uuid survives partial update")
}

func TestPodcastPartialUpdateKeepsFields(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	_, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{
		{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
			Uuid:          "pod-1",
			Subscribed:    wrapperspb.Bool(true),
			AutoStartFrom: wrapperspb.Int32(30),
			FolderUuid:    wrapperspb.String("folder-1"),
		}}},
	}})
	assert.NoError(t, err)

	// a later record with only sortPosition set keeps everything else
	_, err = engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{
		{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
			Uuid:         "pod-1",
			SortPosition: wrapperspb.Int32(5),
		}}},
	}})
	assert.NoError(t, err)

	row := store.podcasts["pod-1"]
	assert.True(t, row.Subscribed)
	assert.Equal(t, int32(30), *row.AutoStartFrom)
	assert.Equal(t, "folder-1", *row.FolderUuid)
	assert.Equal(t, int32(5), *row.SortPosition)

	// unsubscribe via isDeleted tombstone
	_, err = engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{
		{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
			Uuid:      "pod-1",
			IsDeleted: wrapperspb.Bool(true),
		}}},
	}})
	assert.NoError(t, err)
	assert.True(t, store.podcasts["pod-1"].IsDeleted)
	assert.False(t, store.podcasts["pod-1"].Subscribed)
}

func TestBookmarkTitleLWW(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	record := func(title string, modified int64) *pb.Record {
		return &pb.Record{Record: &pb.Record_Bookmark{Bookmark: &pb.SyncUserBookmark{
			BookmarkUuid:  "bm-1",
			PodcastUuid:   "pod-1",
			EpisodeUuid:   "ep-1",
			CreatedAt:     timestamppb.Now(),
			Time:          wrapperspb.Int32(42),
			Title:         wrapperspb.String(title),
			TitleModified: wrapperspb.Int64(modified),
		}}}
	}

	_, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{record("first", 2000)}})
	assert.NoError(t, err)
	_, err = engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{record("stale", 1000)}})
	assert.NoError(t, err)
	assert.Equal(t, "first", store.bookmarks["bm-1"].Title)
	assert.Equal(t, int32(42), store.bookmarks["bm-1"].TimeSecs)

	_, err = engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{record("newer", 3000)}})
	assert.NoError(t, err)
	assert.Equal(t, "newer", store.bookmarks["bm-1"].Title)
}

func upNextChange(action int32, uuid string, modified int64) *pb.UpNextChanges_Change {
	return &pb.UpNextChanges_Change{Uuid: uuid, Action: action, Modified: modified, Title: "t-" + uuid}
}

func queueUuids(store *fakeStore) []string {
	items, _ := store.GetUpNextItems(context.Background(), 1)
	var out []string
	for _, item := range items {
		out = append(out, item.EpisodeUuid)
	}
	return out
}

func TestUpNextActions(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	// build queue: playLast a, playLast b, playNow c => c a b
	resp, err := engine.SyncUpNext(ctx, 1, &pb.UpNextSyncRequest{UpNext: &pb.UpNextChanges{Changes: []*pb.UpNextChanges_Change{
		upNextChange(upNextActionPlayLast, "a", 1),
		upNextChange(upNextActionPlayLast, "b", 2),
		upNextChange(upNextActionPlayNow, "c", 3),
	}}})
	assert.NoError(t, err)
	assert.Equal(t, []string{"c", "a", "b"}, queueUuids(store))
	assert.Greater(t, resp.ServerModified, int64(0))
	assert.Len(t, resp.Episodes, 3)
	firstModified := resp.ServerModified

	// playNext inserts after the head
	_, err = engine.SyncUpNext(ctx, 1, &pb.UpNextSyncRequest{UpNext: &pb.UpNextChanges{Changes: []*pb.UpNextChanges_Change{
		upNextChange(upNextActionPlayNext, "d", 4),
	}}})
	assert.NoError(t, err)
	assert.Equal(t, []string{"c", "d", "a", "b"}, queueUuids(store))

	// remove
	_, err = engine.SyncUpNext(ctx, 1, &pb.UpNextSyncRequest{UpNext: &pb.UpNextChanges{Changes: []*pb.UpNextChanges_Change{
		upNextChange(upNextActionRemove, "a", 5),
	}}})
	assert.NoError(t, err)
	assert.Equal(t, []string{"c", "d", "b"}, queueUuids(store))

	// no changes: queue returned unchanged, serverModified not bumped
	before := store.user.UpNextModified
	resp, err = engine.SyncUpNext(ctx, 1, &pb.UpNextSyncRequest{UpNext: &pb.UpNextChanges{ServerModified: before}})
	assert.NoError(t, err)
	assert.Equal(t, before, resp.ServerModified)
	assert.Len(t, resp.Episodes, 3)
	assert.Greater(t, before, firstModified)

	// replace
	_, err = engine.SyncUpNext(ctx, 1, &pb.UpNextSyncRequest{UpNext: &pb.UpNextChanges{Changes: []*pb.UpNextChanges_Change{
		{Action: upNextActionReplace, Modified: 6, Episodes: []*pb.UpNextEpisodeRequest{
			{Uuid: "x", Title: "X"}, {Uuid: "y", Title: "Y"},
		}},
	}}})
	assert.NoError(t, err)
	assert.Equal(t, []string{"x", "y"}, queueUuids(store))
}

func TestUpNextShowPlayStatus(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	_, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{episodeRecord("a", 123, 1000)}})
	assert.NoError(t, err)

	resp, err := engine.SyncUpNext(ctx, 1, &pb.UpNextSyncRequest{
		ShowPlayStatus: true,
		UpNext: &pb.UpNextChanges{Changes: []*pb.UpNextChanges_Change{
			upNextChange(upNextActionPlayLast, "a", 1),
		}},
	})
	assert.NoError(t, err)
	assert.Len(t, resp.EpisodeSync, 1)
	assert.Equal(t, int32(123), resp.EpisodeSync[0].PlayedUpTo.GetValue())
}

func TestHistoryAddDeleteClearAndCap(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	// add 105 episodes; cap keeps the newest 100
	var changes []*pb.HistoryChange
	for i := 0; i < 105; i++ {
		changes = append(changes, &pb.HistoryChange{
			Action:     historyActionAdd,
			Episode:    uuidLike(i),
			ModifiedAt: int64(1000 + i),
		})
	}
	resp, err := engine.SyncHistory(ctx, 1, &pb.HistorySyncRequest{Changes: changes})
	assert.NoError(t, err)
	assert.Len(t, resp.Changes, 100)
	// newest first
	assert.Equal(t, uuidLike(104), resp.Changes[0].Episode)
	// oldest five trimmed
	assert.Equal(t, uuidLike(5), resp.Changes[99].Episode)
	assert.Greater(t, resp.ServerModified, int64(0))

	// delete one
	resp, err = engine.SyncHistory(ctx, 1, &pb.HistorySyncRequest{Changes: []*pb.HistoryChange{
		{Action: historyActionDelete, Episode: uuidLike(104), ModifiedAt: 2000},
	}})
	assert.NoError(t, err)
	assert.Len(t, resp.Changes, 100)
	assert.Equal(t, int32(historyActionDelete), resp.Changes[0].Action)
	assert.Equal(t, uuidLike(104), resp.Changes[0].Episode)

	// clear all up to a point
	resp, err = engine.SyncHistory(ctx, 1, &pb.HistorySyncRequest{Changes: []*pb.HistoryChange{
		{Action: historyActionClearAll, ModifiedAt: 1200},
	}})
	assert.NoError(t, err)
	assert.Equal(t, int64(1200), resp.LastCleared)
	for _, change := range resp.Changes {
		assert.Greater(t, change.ModifiedAt, int64(1200))
	}

	// read-only request keeps state
	resp2, err := engine.SyncHistory(ctx, 1, &pb.HistorySyncRequest{ServerModified: resp.ServerModified})
	assert.NoError(t, err)
	assert.Equal(t, resp.ServerModified, resp2.ServerModified)
	assert.Len(t, resp2.Changes, len(resp.Changes))
}

func uuidLike(i int) string {
	return string(rune('a'+i/26)) + string(rune('a'+i%26)) + "-episode"
}

func TestSettingsMergeAndResponse(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	setting := func(v int32, modifiedMs int64) *pb.Int32Setting {
		return &pb.Int32Setting{
			Value:      wrapperspb.Int32(v),
			Changed:    wrapperspb.Bool(true),
			ModifiedAt: timestamppb.New(timestampFromMillis(modifiedMs)),
		}
	}

	// initial write
	resp, err := engine.UpdateSettings(ctx, 1, &pb.NamedSettingsRequest{
		ChangedSettings: &pb.ChangeableSettings{SkipForward: setting(30, 2000)},
	})
	assert.NoError(t, err)
	assert.Equal(t, int32(30), resp.SkipForward.Value.GetValue())
	assert.True(t, resp.SkipForward.Changed.GetValue())
	assert.Greater(t, store.user.SyncLastModified, int64(0))
	// only stored keys are populated
	assert.Nil(t, resp.SkipBack)

	// stale update loses
	resp, err = engine.UpdateSettings(ctx, 1, &pb.NamedSettingsRequest{
		ChangedSettings: &pb.ChangeableSettings{SkipForward: setting(45, 1000)},
	})
	assert.NoError(t, err)
	assert.Equal(t, int32(30), resp.SkipForward.Value.GetValue())

	// newer update wins
	resp, err = engine.UpdateSettings(ctx, 1, &pb.NamedSettingsRequest{
		ChangedSettings: &pb.ChangeableSettings{SkipForward: setting(60, 3000)},
	})
	assert.NoError(t, err)
	assert.Equal(t, int32(60), resp.SkipForward.Value.GetValue())

	// legacy shape applies with server time
	resp, err = engine.UpdateSettings(ctx, 1, &pb.NamedSettingsRequest{
		Settings: &pb.NamedSettings{SkipBack: wrapperspb.Int32(15)},
	})
	assert.NoError(t, err)
	assert.Equal(t, int32(15), resp.SkipBack.Value.GetValue())
	assert.Equal(t, int32(60), resp.SkipForward.Value.GetValue(), "existing settings kept")
}

func TestLibraryEndpoints(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	// seed via sync
	_, err := engine.ApplyUpdate(ctx, 1, &pb.SyncUpdateRequest{Records: []*pb.Record{
		episodeRecord("ep-1", 100, 1000),
		{Record: &pb.Record_Episode{Episode: &pb.SyncUserEpisode{
			Uuid: "ep-2", PodcastUuid: "pod-1",
			Starred: wrapperspb.Bool(true), StarredModified: wrapperspb.Int64(1000),
		}}},
		{Record: &pb.Record_Bookmark{Bookmark: &pb.SyncUserBookmark{
			BookmarkUuid: "bm-1", PodcastUuid: "pod-1", EpisodeUuid: "ep-1",
			CreatedAt: timestamppb.Now(), Time: wrapperspb.Int32(10),
			Title: wrapperspb.String("mark"), TitleModified: wrapperspb.Int64(1000),
		}}},
		{Record: &pb.Record_Folder{Folder: &pb.SyncUserFolder{FolderUuid: "f-1", Name: "News", Color: 2}}},
		{Record: &pb.Record_Playlist{Playlist: &pb.SyncUserPlaylist{
			Uuid: "pl-1", Title: wrapperspb.String("Filter"),
			Manual: wrapperspb.Bool(false),
		}}},
	}})
	assert.NoError(t, err)

	episodes, err := engine.PodcastEpisodes(ctx, 1, &pb.UuidRequest{Uuid: "pod-1", IncludeBookmarks: true})
	assert.NoError(t, err)
	assert.Len(t, episodes.Episodes, 2)
	var withBookmark *pb.EpisodeSyncResponse
	for _, ep := range episodes.Episodes {
		if ep.Uuid == "ep-1" {
			withBookmark = ep
		}
	}
	assert.NotNil(t, withBookmark)
	assert.Len(t, withBookmark.Bookmarks, 1)
	assert.Equal(t, "mark", withBookmark.Bookmarks[0].Title)

	starred, err := engine.StarredList(ctx, 1)
	assert.NoError(t, err)
	assert.Len(t, starred.Episodes, 1)
	assert.Equal(t, "ep-2", starred.Episodes[0].Uuid)

	bookmarks, err := engine.BookmarkList(ctx, 1)
	assert.NoError(t, err)
	assert.Len(t, bookmarks.Bookmarks, 1)

	playlists, err := engine.PlaylistList(ctx, 1)
	assert.NoError(t, err)
	assert.Len(t, playlists.Playlists, 1)
	assert.Equal(t, "Filter", playlists.Playlists[0].Title)

	lastSync, err := engine.LastSyncAt(ctx, 1)
	assert.NoError(t, err)
	assert.Greater(t, lastSync.LastSyncAtMs, int64(0))
	assert.NotEmpty(t, lastSync.LastSyncAt)
}

func TestRealtimeEpisodeUpdates(t *testing.T) {
	store := newFakeStore()
	engine := &Engine{DB: store}
	ctx := context.Background()

	err := engine.UpdateEpisode(ctx, 1, &pb.UpdateEpisodeRequest{
		Uuid: "ep-1", Podcast: "pod-1",
		Position: wrapperspb.Int32(555), Status: 2, Duration: 1800,
	})
	assert.NoError(t, err)
	assert.Equal(t, int64(555), store.episodes["ep-1"].PlayedUpTo)
	assert.Equal(t, int32(2), store.episodes["ep-1"].PlayingStatus)
	assert.Equal(t, int64(1800), store.episodes["ep-1"].Duration)
	assert.Greater(t, store.user.SyncLastModified, int64(0))

	err = engine.UpdateEpisodeStar(ctx, 1, &pb.UpdateEpisodeStarRequest{Uuid: "ep-1", Podcast: "pod-1", Star: true})
	assert.NoError(t, err)
	assert.True(t, store.episodes["ep-1"].Starred)
	assert.Equal(t, int64(555), store.episodes["ep-1"].PlayedUpTo, "star must not clobber position")
}
