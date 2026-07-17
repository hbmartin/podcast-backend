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
	InsertFeedback(ctx context.Context, arg InsertFeedbackParams) error
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

	UpsertDevicePush(ctx context.Context, arg UpsertDevicePushParams) error
	SetPodcastNotifyFlags(ctx context.Context, arg SetPodcastNotifyFlagsParams) error
	GetPushTargetsForPodcast(ctx context.Context, podcastUuid string) ([]GetPushTargetsForPodcastRow, error)
	ClearPushToken(ctx context.Context, pushToken string) error
	InsertChallenge(ctx context.Context, arg InsertChallengeParams) error
	ConsumeChallenge(ctx context.Context, challenge []byte) ([]byte, error)
	DeleteExpiredChallenges(ctx context.Context) error
	InsertAttestKey(ctx context.Context, arg InsertAttestKeyParams) error
	GetAttestKey(ctx context.Context, keyID string) (AttestKey, error)
	AdvanceAttestCounter(ctx context.Context, arg AdvanceAttestCounterParams) (int64, error)

	InsertTranscriptContribution(ctx context.Context, arg InsertTranscriptContributionParams) error
	CountRecentContributionsByAttribution(ctx context.Context, arg CountRecentContributionsByAttributionParams) (int64, error)
	InsertTranscriptSighting(ctx context.Context, arg InsertTranscriptSightingParams) (int64, error)
	CountRecentSightingsByAttribution(ctx context.Context, arg CountRecentSightingsByAttributionParams) (int64, error)
	GetTranscriptSighting(ctx context.Context, id int64) (TranscriptSighting, error)
	UpdateSightingContent(ctx context.Context, arg UpdateSightingContentParams) error
	MarkSightingStatus(ctx context.Context, arg MarkSightingStatusParams) error

	GetHandleStatus(ctx context.Context, handle string) (GetHandleStatusRow, error)
	ClaimHandle(ctx context.Context, arg ClaimHandleParams) error
	CreateSocialProfile(ctx context.Context, arg CreateSocialProfileParams) (SocialProfile, error)
	GetSocialProfileByUserID(ctx context.Context, userID int64) (SocialProfile, error)
	GetSocialProfileByHandle(ctx context.Context, handle string) (SocialProfile, error)
	UpdateSocialProfile(ctx context.Context, arg UpdateSocialProfileParams) (SocialProfile, error)
	UpsertSocialRelationship(ctx context.Context, arg UpsertSocialRelationshipParams) error
	DeleteSocialRelationship(ctx context.Context, arg DeleteSocialRelationshipParams) (int64, error)
	IsBlockedEither(ctx context.Context, arg IsBlockedEitherParams) (bool, error)
	InsertModerationReport(ctx context.Context, arg InsertModerationReportParams) error
	DeleteSocialProfile(ctx context.Context, userID int64) (int64, error)
	TombstoneHandle(ctx context.Context, userID *int64) (int64, error)
	DeleteRelationshipsForUser(ctx context.Context, userID int64) error
	GetPublicFollowedShows(ctx context.Context, arg GetPublicFollowedShowsParams) ([]GetPublicFollowedShowsRow, error)
	GetPublicTopPodcasts(ctx context.Context, arg GetPublicTopPodcastsParams) ([]GetPublicTopPodcastsRow, error)
	GetPublicRecentlyPlayed(ctx context.Context, arg GetPublicRecentlyPlayedParams) ([]GetPublicRecentlyPlayedRow, error)

	UpsertPodcastReview(ctx context.Context, arg UpsertPodcastReviewParams) (PodcastReview, error)
	DeletePodcastReview(ctx context.Context, arg DeletePodcastReviewParams) (int64, error)
	DeleteReviewsForUser(ctx context.Context, userID int64) error
	GetPodcastReviews(ctx context.Context, arg GetPodcastReviewsParams) ([]GetPodcastReviewsRow, error)
	CountPodcastReviews(ctx context.Context, podcastUuid string) (int64, error)
	GetOwnPodcastReview(ctx context.Context, arg GetOwnPodcastReviewParams) (GetOwnPodcastReviewRow, error)
	CountPlayedEpisodesOfPodcast(ctx context.Context, arg CountPlayedEpisodesOfPodcastParams) (int64, error)
	UpsertEpisodeReaction(ctx context.Context, arg UpsertEpisodeReactionParams) error
	DeleteEpisodeReaction(ctx context.Context, arg DeleteEpisodeReactionParams) (int64, error)
	GetEpisodeReactionCounts(ctx context.Context, episodeUuid string) ([]GetEpisodeReactionCountsRow, error)
	GetOwnEpisodeReaction(ctx context.Context, arg GetOwnEpisodeReactionParams) (int16, error)

	UpsertFollow(ctx context.Context, arg UpsertFollowParams) error
	DeleteFollow(ctx context.Context, arg DeleteFollowParams) (int64, error)
	ApproveFollow(ctx context.Context, arg ApproveFollowParams) (int64, error)
	CountCommentReplies(ctx context.Context, parentID *int64) (int64, error)
	CountEpisodeComments(ctx context.Context, episodeUuid string) (int64, error)
	CountInboxReplies(ctx context.Context, userID *int64) (int64, error)
	CountUnreadInboxReplies(ctx context.Context, userID *int64) (int64, error)
	EditComment(ctx context.Context, arg EditCommentParams) (int64, error)
	GetCommentByID(ctx context.Context, id int64) (GetCommentByIDRow, error)
	GetCommentReplies(ctx context.Context, arg GetCommentRepliesParams) ([]GetCommentRepliesRow, error)
	GetEpisodeComments(ctx context.Context, arg GetEpisodeCommentsParams) ([]GetEpisodeCommentsRow, error)
	GetEpisodePlaybackForGate(ctx context.Context, arg GetEpisodePlaybackForGateParams) (GetEpisodePlaybackForGateRow, error)
	GetInboxReplies(ctx context.Context, arg GetInboxRepliesParams) ([]GetInboxRepliesRow, error)
	HasSocialRelationship(ctx context.Context, arg HasSocialRelationshipParams) (bool, error)
	InsertComment(ctx context.Context, arg InsertCommentParams) (InsertCommentRow, error)
	SetRepliesSeen(ctx context.Context, userID int64) error
	TombstoneComment(ctx context.Context, arg TombstoneCommentParams) (int64, error)
	TombstoneCommentsForUser(ctx context.Context, userID *int64) error
	GetFollowState(ctx context.Context, arg GetFollowStateParams) (int16, error)
	CountFollowers(ctx context.Context, followeeUserID int64) (int64, error)
	CountFollowing(ctx context.Context, followerUserID int64) (int64, error)
	GetFollowers(ctx context.Context, arg GetFollowersParams) ([]GetFollowersRow, error)
	GetFollowing(ctx context.Context, arg GetFollowingParams) ([]GetFollowingRow, error)
	GetPendingFollowRequests(ctx context.Context, arg GetPendingFollowRequestsParams) ([]GetPendingFollowRequestsRow, error)
	DeleteFollowsForUser(ctx context.Context, userID int64) error
	GetFeedItems(ctx context.Context, arg GetFeedItemsParams) ([]GetFeedItemsRow, error)

	InsertSharedItem(ctx context.Context, arg InsertSharedItemParams) (int64, error)
	GetInboxItems(ctx context.Context, arg GetInboxItemsParams) ([]GetInboxItemsRow, error)
	CountInboxItems(ctx context.Context, recipientUserID int64) (int64, error)
	CountUnreadInboxItems(ctx context.Context, recipientUserID int64) (int64, error)
	MarkInboxItemsRead(ctx context.Context, arg MarkInboxItemsReadParams) error
	DeleteInboxItem(ctx context.Context, arg DeleteInboxItemParams) (int64, error)
	DeleteSharedItemsForUser(ctx context.Context, userID int64) error
}
