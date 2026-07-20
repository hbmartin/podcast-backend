package syncsvc

import (
	"context"
	"sort"

	"github.com/hbmartin/podcast-backend/db"

	"github.com/jackc/pgx/v5"
)

// fakeStore is an in-memory db.Store covering the queries the sync engine
// uses. Methods not implemented here come from the embedded interface and
// panic if called.
type fakeStore struct {
	db.Store

	user        db.User
	podcasts    map[string]db.UserPodcast
	episodes    map[string]db.UserEpisode
	folders     map[string]db.Folder
	playlists   map[string]db.Playlist
	bookmarks   map[string]db.Bookmark
	devices     map[string]db.UpsertDeviceParams
	upNext      []db.UpNextItem
	history     map[string]db.History
	settings    *db.UserSetting
	catalogRows []db.GetSubscribedPodcastsWithCatalogRow
}

func (f *fakeStore) CountMilestonesForUser(ctx context.Context, userID int64) (int64, error) {
	return 1, nil
}

func (f *fakeStore) InsertMilestoneBackdated(ctx context.Context, arg db.InsertMilestoneBackdatedParams) (int64, error) {
	return 1, nil
}

func (f *fakeStore) GetListeningTotals(ctx context.Context, userID int64) (db.GetListeningTotalsRow, error) {
	return db.GetListeningTotalsRow{}, nil
}

func (f *fakeStore) InsertMilestone(ctx context.Context, arg db.InsertMilestoneParams) (int64, error) {
	return 1, nil
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		user:      db.User{ID: 1, Uuid: "user-uuid", Email: "a@b.co"},
		podcasts:  map[string]db.UserPodcast{},
		episodes:  map[string]db.UserEpisode{},
		folders:   map[string]db.Folder{},
		playlists: map[string]db.Playlist{},
		bookmarks: map[string]db.Bookmark{},
		devices:   map[string]db.UpsertDeviceParams{},
		history:   map[string]db.History{},
	}
}

func (f *fakeStore) InTx(ctx context.Context, fn func(db.Querier) error) error {
	return fn(f)
}

func (f *fakeStore) GetUserForUpdate(ctx context.Context, id int64) (db.User, error) {
	return f.user, nil
}

func (f *fakeStore) GetUserByID(ctx context.Context, id int64) (db.User, error) {
	return f.user, nil
}

func (f *fakeStore) SetUserSyncLastModified(ctx context.Context, arg db.SetUserSyncLastModifiedParams) error {
	f.user.SyncLastModified = arg.SyncLastModified
	return nil
}

func (f *fakeStore) SetUserUpNextModified(ctx context.Context, arg db.SetUserUpNextModifiedParams) error {
	f.user.UpNextModified = arg.UpNextModified
	return nil
}

func (f *fakeStore) SetUserHistoryModified(ctx context.Context, arg db.SetUserHistoryModifiedParams) error {
	f.user.HistoryModified = arg.HistoryModified
	return nil
}

func (f *fakeStore) SetUserHistoryCleared(ctx context.Context, arg db.SetUserHistoryClearedParams) error {
	f.user.HistoryClearedAtMs = arg.HistoryClearedAtMs
	f.user.HistoryModified = arg.HistoryModified
	return nil
}

func (f *fakeStore) GetUserPodcast(ctx context.Context, arg db.GetUserPodcastParams) (db.UserPodcast, error) {
	row, ok := f.podcasts[arg.PodcastUuid]
	if !ok {
		return db.UserPodcast{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeStore) UpsertUserPodcast(ctx context.Context, arg db.UpsertUserPodcastParams) error {
	// preserve fields the upsert doesn't touch (notify_enabled)
	row := f.podcasts[arg.PodcastUuid]
	f.podcasts[arg.PodcastUuid] = db.UserPodcast{
		UserID:            arg.UserID,
		PodcastUuid:       arg.PodcastUuid,
		Subscribed:        arg.Subscribed,
		IsDeleted:         arg.IsDeleted,
		AutoStartFrom:     arg.AutoStartFrom,
		AutoSkipLast:      arg.AutoSkipLast,
		EpisodesSortOrder: arg.EpisodesSortOrder,
		FolderUuid:        arg.FolderUuid,
		SortPosition:      arg.SortPosition,
		DateAdded:         arg.DateAdded,
		Settings:          arg.Settings,
		ModifiedAt:        arg.ModifiedAt,
		NotifyEnabled:     row.NotifyEnabled,
	}
	return nil
}

func (f *fakeStore) GetUserPodcastsModifiedSince(ctx context.Context, arg db.GetUserPodcastsModifiedSinceParams) ([]db.UserPodcast, error) {
	var out []db.UserPodcast
	for _, row := range f.podcasts {
		if row.ModifiedAt > arg.ModifiedAt && row.ModifiedAt <= arg.ModifiedAt_2 {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PodcastUuid < out[j].PodcastUuid })
	return out, nil
}

func (f *fakeStore) GetSubscribedPodcastsWithCatalog(ctx context.Context, userID int64) ([]db.GetSubscribedPodcastsWithCatalogRow, error) {
	return f.catalogRows, nil
}

func (f *fakeStore) GetUserEpisode(ctx context.Context, arg db.GetUserEpisodeParams) (db.UserEpisode, error) {
	row, ok := f.episodes[arg.EpisodeUuid]
	if !ok {
		return db.UserEpisode{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeStore) UpsertUserEpisode(ctx context.Context, arg db.UpsertUserEpisodeParams) error {
	f.episodes[arg.EpisodeUuid] = db.UserEpisode(arg)
	return nil
}

func (f *fakeStore) GetUserEpisodesModifiedSince(ctx context.Context, arg db.GetUserEpisodesModifiedSinceParams) ([]db.UserEpisode, error) {
	var out []db.UserEpisode
	for _, row := range f.episodes {
		if row.ModifiedAt > arg.ModifiedAt && row.ModifiedAt <= arg.ModifiedAt_2 {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EpisodeUuid < out[j].EpisodeUuid })
	return out, nil
}

func (f *fakeStore) GetUserEpisodesForPodcast(ctx context.Context, arg db.GetUserEpisodesForPodcastParams) ([]db.UserEpisode, error) {
	var out []db.UserEpisode
	for _, row := range f.episodes {
		if row.PodcastUuid == arg.PodcastUuid {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EpisodeUuid < out[j].EpisodeUuid })
	return out, nil
}

func (f *fakeStore) GetStarredEpisodes(ctx context.Context, userID int64) ([]db.UserEpisode, error) {
	var out []db.UserEpisode
	for _, row := range f.episodes {
		if row.Starred {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EpisodeUuid < out[j].EpisodeUuid })
	return out, nil
}

func (f *fakeStore) GetFolder(ctx context.Context, arg db.GetFolderParams) (db.Folder, error) {
	row, ok := f.folders[arg.FolderUuid]
	if !ok {
		return db.Folder{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeStore) UpsertFolder(ctx context.Context, arg db.UpsertFolderParams) error {
	f.folders[arg.FolderUuid] = db.Folder(arg)
	return nil
}

func (f *fakeStore) GetFoldersModifiedSince(ctx context.Context, arg db.GetFoldersModifiedSinceParams) ([]db.Folder, error) {
	var out []db.Folder
	for _, row := range f.folders {
		if row.ModifiedAt > arg.ModifiedAt && row.ModifiedAt <= arg.ModifiedAt_2 {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeStore) GetFolders(ctx context.Context, userID int64) ([]db.Folder, error) {
	var out []db.Folder
	for _, row := range f.folders {
		if !row.IsDeleted {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeStore) GetPlaylist(ctx context.Context, arg db.GetPlaylistParams) (db.Playlist, error) {
	row, ok := f.playlists[arg.Uuid]
	if !ok {
		return db.Playlist{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeStore) UpsertPlaylist(ctx context.Context, arg db.UpsertPlaylistParams) error {
	f.playlists[arg.Uuid] = db.Playlist(arg)
	return nil
}

func (f *fakeStore) GetPlaylistsModifiedSince(ctx context.Context, arg db.GetPlaylistsModifiedSinceParams) ([]db.Playlist, error) {
	var out []db.Playlist
	for _, row := range f.playlists {
		if row.ModifiedAt > arg.ModifiedAt && row.ModifiedAt <= arg.ModifiedAt_2 {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeStore) GetPlaylists(ctx context.Context, userID int64) ([]db.Playlist, error) {
	var out []db.Playlist
	for _, row := range f.playlists {
		if !row.IsDeleted {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeStore) GetBookmark(ctx context.Context, arg db.GetBookmarkParams) (db.Bookmark, error) {
	row, ok := f.bookmarks[arg.BookmarkUuid]
	if !ok {
		return db.Bookmark{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeStore) UpsertBookmark(ctx context.Context, arg db.UpsertBookmarkParams) error {
	f.bookmarks[arg.BookmarkUuid] = db.Bookmark(arg)
	return nil
}

func (f *fakeStore) GetBookmarksModifiedSince(ctx context.Context, arg db.GetBookmarksModifiedSinceParams) ([]db.Bookmark, error) {
	var out []db.Bookmark
	for _, row := range f.bookmarks {
		if row.ModifiedAt > arg.ModifiedAt && row.ModifiedAt <= arg.ModifiedAt_2 {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeStore) GetBookmarks(ctx context.Context, userID int64) ([]db.Bookmark, error) {
	var out []db.Bookmark
	for _, row := range f.bookmarks {
		if !row.IsDeleted {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeStore) GetBookmarksForEpisodes(ctx context.Context, arg db.GetBookmarksForEpisodesParams) ([]db.Bookmark, error) {
	include := map[string]bool{}
	for _, uuid := range arg.Column2 {
		include[uuid] = true
	}
	var out []db.Bookmark
	for _, row := range f.bookmarks {
		if include[row.EpisodeUuid] && !row.IsDeleted {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeStore) UpsertDevice(ctx context.Context, arg db.UpsertDeviceParams) error {
	f.devices[arg.DeviceID] = arg
	return nil
}

func (f *fakeStore) GetUpNextItems(ctx context.Context, userID int64) ([]db.UpNextItem, error) {
	out := append([]db.UpNextItem(nil), f.upNext...)
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

func (f *fakeStore) DeleteAllUpNextItems(ctx context.Context, userID int64) error {
	f.upNext = nil
	return nil
}

func (f *fakeStore) InsertUpNextItem(ctx context.Context, arg db.InsertUpNextItemParams) error {
	f.upNext = append(f.upNext, db.UpNextItem{
		UserID:      arg.UserID,
		EpisodeUuid: arg.EpisodeUuid,
		PodcastUuid: arg.PodcastUuid,
		Title:       arg.Title,
		Url:         arg.Url,
		Published:   arg.Published,
		Position:    arg.Position,
	})
	return nil
}

func (f *fakeStore) UpsertHistoryItem(ctx context.Context, arg db.UpsertHistoryItemParams) error {
	existing, ok := f.history[arg.EpisodeUuid]
	if ok && arg.ModifiedAt <= existing.ModifiedAt {
		return nil
	}
	f.history[arg.EpisodeUuid] = db.History(arg)
	return nil
}

func (f *fakeStore) DeleteHistoryItem(ctx context.Context, arg db.DeleteHistoryItemParams) error {
	delete(f.history, arg.EpisodeUuid)
	return nil
}

func (f *fakeStore) DeleteHistoryBefore(ctx context.Context, arg db.DeleteHistoryBeforeParams) error {
	for uuid, row := range f.history {
		if row.ModifiedAt <= arg.ModifiedAt {
			delete(f.history, uuid)
		}
	}
	return nil
}

func (f *fakeStore) TrimHistory(ctx context.Context, arg db.TrimHistoryParams) error {
	rows := f.historySorted()
	if int64(len(rows)) <= int64(arg.Limit) {
		return nil
	}
	for _, row := range rows[arg.Limit:] {
		delete(f.history, row.EpisodeUuid)
	}
	return nil
}

func (f *fakeStore) GetHistory(ctx context.Context, arg db.GetHistoryParams) ([]db.History, error) {
	rows := f.historySorted()
	if int64(len(rows)) > int64(arg.Limit) {
		rows = rows[:arg.Limit]
	}
	return rows, nil
}

func (f *fakeStore) historySorted() []db.History {
	var rows []db.History
	for _, row := range f.history {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ModifiedAt > rows[j].ModifiedAt })
	return rows
}

func (f *fakeStore) GetUserSettings(ctx context.Context, userID int64) (db.UserSetting, error) {
	if f.settings == nil {
		return db.UserSetting{}, pgx.ErrNoRows
	}
	return *f.settings, nil
}

func (f *fakeStore) UpsertUserSettings(ctx context.Context, arg db.UpsertUserSettingsParams) error {
	f.settings = &db.UserSetting{UserID: arg.UserID, Settings: arg.Settings, ModifiedAt: arg.ModifiedAt}
	return nil
}
