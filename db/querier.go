package db

import "context"

// Querier is the dependency-injection seam between handlers/services and the
// sqlc-generated queries. Keep it in sync with queries.sql.go; tests provide
// hand-written mocks/fakes.
type Querier interface {
	PingDb(ctx context.Context) (int32, error)

	CreateUser(ctx context.Context, arg CreateUserParams) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)
	GetUserByUUID(ctx context.Context, uuid string) (User, error)
	GetUserByID(ctx context.Context, id int64) (User, error)
	UpdateUserEmail(ctx context.Context, arg UpdateUserEmailParams) (int64, error)
	UpdateUserPassword(ctx context.Context, arg UpdateUserPasswordParams) (int64, error)
	SoftDeleteUser(ctx context.Context, id int64) (int64, error)

	CreateRefreshToken(ctx context.Context, arg CreateRefreshTokenParams) (RefreshToken, error)
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, tokenHash string) (int64, error)
	RevokeAllRefreshTokens(ctx context.Context, userID int64) (int64, error)

	GetUserForUpdate(ctx context.Context, id int64) (User, error)
	SetUserSyncLastModified(ctx context.Context, arg SetUserSyncLastModifiedParams) error
	SetUserUpNextModified(ctx context.Context, arg SetUserUpNextModifiedParams) error
	SetUserHistoryModified(ctx context.Context, arg SetUserHistoryModifiedParams) error
	SetUserHistoryCleared(ctx context.Context, arg SetUserHistoryClearedParams) error

	GetUserPodcast(ctx context.Context, arg GetUserPodcastParams) (UserPodcast, error)
	UpsertUserPodcast(ctx context.Context, arg UpsertUserPodcastParams) error
	GetUserPodcastsModifiedSince(ctx context.Context, arg GetUserPodcastsModifiedSinceParams) ([]UserPodcast, error)
	GetSubscribedPodcastsWithCatalog(ctx context.Context, userID int64) ([]GetSubscribedPodcastsWithCatalogRow, error)

	GetUserEpisode(ctx context.Context, arg GetUserEpisodeParams) (UserEpisode, error)
	UpsertUserEpisode(ctx context.Context, arg UpsertUserEpisodeParams) error
	GetUserEpisodesModifiedSince(ctx context.Context, arg GetUserEpisodesModifiedSinceParams) ([]UserEpisode, error)
	GetUserEpisodesForPodcast(ctx context.Context, arg GetUserEpisodesForPodcastParams) ([]UserEpisode, error)
	GetStarredEpisodes(ctx context.Context, userID int64) ([]UserEpisode, error)

	GetFolder(ctx context.Context, arg GetFolderParams) (Folder, error)
	UpsertFolder(ctx context.Context, arg UpsertFolderParams) error
	GetFoldersModifiedSince(ctx context.Context, arg GetFoldersModifiedSinceParams) ([]Folder, error)
	GetFolders(ctx context.Context, userID int64) ([]Folder, error)

	GetPlaylist(ctx context.Context, arg GetPlaylistParams) (Playlist, error)
	UpsertPlaylist(ctx context.Context, arg UpsertPlaylistParams) error
	GetPlaylistsModifiedSince(ctx context.Context, arg GetPlaylistsModifiedSinceParams) ([]Playlist, error)
	GetPlaylists(ctx context.Context, userID int64) ([]Playlist, error)

	GetBookmark(ctx context.Context, arg GetBookmarkParams) (Bookmark, error)
	UpsertBookmark(ctx context.Context, arg UpsertBookmarkParams) error
	GetBookmarksModifiedSince(ctx context.Context, arg GetBookmarksModifiedSinceParams) ([]Bookmark, error)
	GetBookmarks(ctx context.Context, userID int64) ([]Bookmark, error)
	GetBookmarksForEpisodes(ctx context.Context, arg GetBookmarksForEpisodesParams) ([]Bookmark, error)

	UpsertDevice(ctx context.Context, arg UpsertDeviceParams) error

	GetUpNextItems(ctx context.Context, userID int64) ([]UpNextItem, error)
	DeleteAllUpNextItems(ctx context.Context, userID int64) error
	InsertUpNextItem(ctx context.Context, arg InsertUpNextItemParams) error

	UpsertHistoryItem(ctx context.Context, arg UpsertHistoryItemParams) error
	DeleteHistoryItem(ctx context.Context, arg DeleteHistoryItemParams) error
	DeleteHistoryBefore(ctx context.Context, arg DeleteHistoryBeforeParams) error
	TrimHistory(ctx context.Context, arg TrimHistoryParams) error
	GetHistory(ctx context.Context, arg GetHistoryParams) ([]History, error)

	GetUserSettings(ctx context.Context, userID int64) (UserSetting, error)
	UpsertUserSettings(ctx context.Context, arg UpsertUserSettingsParams) error

	CreatePodcastPending(ctx context.Context, arg CreatePodcastPendingParams) (Podcast, error)
	GetPodcastByUUID(ctx context.Context, uuid string) (Podcast, error)
	GetPodcastByFeedURL(ctx context.Context, feedUrl string) (Podcast, error)
	GetPodcastsByUUIDs(ctx context.Context, uuids []string) ([]Podcast, error)
	GetDuePodcasts(ctx context.Context, limit int32) ([]Podcast, error)
	PodcastHasSubscribers(ctx context.Context, podcastUuid string) (bool, error)
	UpdatePodcastCrawlSuccess(ctx context.Context, arg UpdatePodcastCrawlSuccessParams) error
	UpdatePodcastCrawlNotModified(ctx context.Context, arg UpdatePodcastCrawlNotModifiedParams) error
	UpdatePodcastCrawlFailure(ctx context.Context, arg UpdatePodcastCrawlFailureParams) error

	GetPodcastByID(ctx context.Context, id int64) (Podcast, error)
	UpdatePodcastColors(ctx context.Context, arg UpdatePodcastColorsParams) error
	TopPodcastsBySubscribers(ctx context.Context, limit int32) ([]TopPodcastsBySubscribersRow, error)
	RecentPodcasts(ctx context.Context, limit int32) ([]Podcast, error)
	DistinctCategories(ctx context.Context) ([]DistinctCategoriesRow, error)
	PodcastsByCategory(ctx context.Context, arg PodcastsByCategoryParams) ([]Podcast, error)
	SearchPodcasts(ctx context.Context, arg SearchPodcastsParams) ([]Podcast, error)
	SearchEpisodesGlobal(ctx context.Context, arg SearchEpisodesGlobalParams) ([]SearchEpisodesGlobalRow, error)
	SearchEpisodesInPodcast(ctx context.Context, arg SearchEpisodesInPodcastParams) ([]Episode, error)

	UpsertEpisode(ctx context.Context, arg UpsertEpisodeParams) error
	GetEpisodesByPodcastID(ctx context.Context, arg GetEpisodesByPodcastIDParams) ([]Episode, error)
	GetEpisodeByUUID(ctx context.Context, uuid string) (Episode, error)
	GetEpisodesPublishedAfter(ctx context.Context, arg GetEpisodesPublishedAfterParams) ([]Episode, error)

	UpsertPodcastRating(ctx context.Context, arg UpsertPodcastRatingParams) error
	GetPodcastRating(ctx context.Context, arg GetPodcastRatingParams) (PodcastRating, error)
	GetUserPodcastRatings(ctx context.Context, userID int64) ([]PodcastRating, error)
	GetPodcastRatingAggregate(ctx context.Context, podcastUuid string) (GetPodcastRatingAggregateRow, error)

	GetDevice(ctx context.Context, arg GetDeviceParams) (Device, error)
	GetUserStatsTotals(ctx context.Context, userID int64) (GetUserStatsTotalsRow, error)

	CreateSharedList(ctx context.Context, arg CreateSharedListParams) (SharedList, error)
	GetSharedListByCode(ctx context.Context, code string) (SharedList, error)
}
