package syncsvc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Engine applies sync mutations and builds sync responses. All mutating
// entry points serialize per-user via a row lock on the users row.
type Engine struct {
	DB db.Store
}

// OnUnknownPodcast is the sync-ingestion hook (Slice 11): called with a feed
// URL whenever a synced subscription references a podcast the catalog has
// never seen. Wired by main; nil-safe.
var OnUnknownPodcast func(feedURL string)

// ApplyUpdate implements POST user/sync/update: applies the request's records
// under a fresh sync token and returns every record changed after the
// client's lastModified (including echoes of the records just applied — the
// client import is idempotent).
func (e *Engine) ApplyUpdate(ctx context.Context, userID int64, req *pb.SyncUpdateRequest) (*pb.SyncUpdateResponse, error) {
	const op errs.Op = "syncsvc/Engine.ApplyUpdate"

	var resp *pb.SyncUpdateResponse
	err := e.DB.InTx(ctx, func(q db.Querier) error {
		user, err := q.GetUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}

		token := NextToken(user.SyncLastModified)

		for _, record := range req.Records {
			if err := applyRecord(ctx, q, userID, token, record); err != nil {
				return err
			}
		}

		if err := q.SetUserSyncLastModified(ctx, db.SetUserSyncLastModifiedParams{ID: userID, SyncLastModified: token}); err != nil {
			return err
		}

		records, err := collectChangedRecords(ctx, q, userID, req.LastModified, token)
		if err != nil {
			return err
		}

		resp = &pb.SyncUpdateResponse{LastModified: token, Records: records}
		return nil
	})
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}
	return resp, nil
}

func applyRecord(ctx context.Context, q db.Querier, userID int64, token int64, record *pb.Record) error {
	switch rec := record.Record.(type) {
	case *pb.Record_Podcast:
		return applyPodcastRecord(ctx, q, userID, token, rec.Podcast)
	case *pb.Record_Episode:
		return applyEpisodeRecord(ctx, q, userID, token, rec.Episode)
	case *pb.Record_Playlist:
		return applyPlaylistRecord(ctx, q, userID, token, rec.Playlist)
	case *pb.Record_Folder:
		return applyFolderRecord(ctx, q, userID, token, rec.Folder)
	case *pb.Record_Bookmark:
		return applyBookmarkRecord(ctx, q, userID, token, rec.Bookmark)
	case *pb.Record_Device:
		return applyDeviceRecord(ctx, q, userID, rec.Device)
	default:
		slog.Warn("Unknown sync record type ignored")
		return nil
	}
}

// applyPodcastRecord upserts a subscription. Absent wrapper fields keep the
// stored values (partial update).
func applyPodcastRecord(ctx context.Context, q db.Querier, userID int64, token int64, rec *pb.SyncUserPodcast) error {
	if rec.Uuid == "" {
		return nil
	}

	existing, err := q.GetUserPodcast(ctx, db.GetUserPodcastParams{UserID: userID, PodcastUuid: rec.Uuid})
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	params := db.UpsertUserPodcastParams{
		UserID:      userID,
		PodcastUuid: rec.Uuid,
		Subscribed:  true,
		Settings:    []byte("{}"),
		ModifiedAt:  token,
	}
	if found {
		params.Subscribed = existing.Subscribed
		params.IsDeleted = existing.IsDeleted
		params.AutoStartFrom = existing.AutoStartFrom
		params.AutoSkipLast = existing.AutoSkipLast
		params.EpisodesSortOrder = existing.EpisodesSortOrder
		params.FolderUuid = existing.FolderUuid
		params.SortPosition = existing.SortPosition
		params.DateAdded = existing.DateAdded
		params.Settings = existing.Settings
	}

	if rec.Subscribed != nil {
		params.Subscribed = rec.Subscribed.Value
	}
	if rec.IsDeleted != nil {
		params.IsDeleted = rec.IsDeleted.Value
		if rec.IsDeleted.Value {
			params.Subscribed = false
		}
	}
	if found {
		params.SyncedTitle = existing.SyncedTitle
		params.SyncedFeedUrl = existing.SyncedFeedUrl
	}
	if title := rec.GetTitle().GetValue(); title != "" {
		params.SyncedTitle = title
	}
	if feedURL := rec.GetFeedUrl().GetValue(); feedURL != "" {
		params.SyncedFeedUrl = feedURL
		// Unknown in the catalog: hand the URL to the ingestion hook so the
		// crawl fills catalog/episodes/artwork (Slice 11, QA follow-up).
		if _, catErr := q.GetPodcastByUUID(ctx, rec.Uuid); errors.Is(catErr, pgx.ErrNoRows) && OnUnknownPodcast != nil {
			OnUnknownPodcast(feedURL)
		}
	}
	if rec.AutoStartFrom != nil {
		params.AutoStartFrom = int32Ptr(rec.AutoStartFrom)
	}
	if rec.AutoSkipLast != nil {
		params.AutoSkipLast = int32Ptr(rec.AutoSkipLast)
	}
	if rec.EpisodesSortOrder != nil {
		params.EpisodesSortOrder = int32Ptr(rec.EpisodesSortOrder)
	}
	if rec.FolderUuid != nil {
		params.FolderUuid = stringPtr(rec.FolderUuid)
	}
	if rec.SortPosition != nil {
		params.SortPosition = int32Ptr(rec.SortPosition)
	}
	if rec.DateAdded != nil {
		params.DateAdded = timePtrFromProto(rec.DateAdded)
	}
	if rec.Settings != nil {
		merged, err := MergePodcastSettings(params.Settings, rec.Settings)
		if err != nil {
			return err
		}
		params.Settings = merged
	}

	return q.UpsertUserPodcast(ctx, params)
}

// applyEpisodeRecord applies per-field last-writer-wins using the client's
// *Modified device-time tokens.
func applyEpisodeRecord(ctx context.Context, q db.Querier, userID int64, token int64, rec *pb.SyncUserEpisode) error {
	if rec.Uuid == "" {
		return nil
	}

	existing, err := q.GetUserEpisode(ctx, db.GetUserEpisodeParams{UserID: userID, EpisodeUuid: rec.Uuid})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		existing = db.UserEpisode{UserID: userID, EpisodeUuid: rec.Uuid, PlayingStatus: 1}
	}

	params := upsertParamsFromRow(existing)
	params.UserID = userID
	params.EpisodeUuid = rec.Uuid
	params.ModifiedAt = token
	if rec.PodcastUuid != "" {
		params.PodcastUuid = rec.PodcastUuid
	}
	// A brand-new row with no podcast uuid cannot satisfy the NOT NULL uuid
	// column — skip the record instead of poisoning the whole batch (QA
	// finding; the client will resend once it knows the podcast).
	if params.PodcastUuid == "" {
		return nil
	}

	if rec.PlayingStatus != nil && modifiedAfter(rec.PlayingStatusModified, existing.PlayingStatusModified) {
		params.PlayingStatus = rec.PlayingStatus.Value
		params.PlayingStatusModified = rec.PlayingStatusModified.Value
	}
	if rec.PlayedUpTo != nil && modifiedAfter(rec.PlayedUpToModified, existing.PlayedUpToModified) {
		params.PlayedUpTo = rec.PlayedUpTo.Value
		params.PlayedUpToModified = rec.PlayedUpToModified.Value
	}
	if rec.Starred != nil && modifiedAfter(rec.StarredModified, existing.StarredModified) {
		params.Starred = rec.Starred.Value
		params.StarredModified = rec.StarredModified.Value
	}
	if rec.IsDeleted != nil && modifiedAfter(rec.IsDeletedModified, existing.IsDeletedModified) {
		params.IsDeleted = rec.IsDeleted.Value
		params.IsDeletedModified = rec.IsDeletedModified.Value
	}
	if rec.Duration != nil && modifiedAfter(rec.DurationModified, existing.DurationModified) {
		params.Duration = rec.Duration.Value
		params.DurationModified = rec.DurationModified.Value
	}
	if rec.DeselectedChaptersModified != nil && modifiedAfter(rec.DeselectedChaptersModified, existing.DeselectedChaptersModified) {
		params.DeselectedChapters = rec.DeselectedChapters
		params.DeselectedChaptersModified = rec.DeselectedChaptersModified.Value
	}

	return q.UpsertUserEpisode(ctx, params)
}

func modifiedAfter(incoming *wrapperspb.Int64Value, existing int64) bool {
	return incoming != nil && incoming.Value > existing
}

func applyPlaylistRecord(ctx context.Context, q db.Querier, userID int64, token int64, rec *pb.SyncUserPlaylist) error {
	if rec.Uuid == "" {
		return nil
	}

	episodes, err := encodePlaylistEpisodes(rec.Episodes)
	if err != nil {
		return err
	}

	params := db.UpsertPlaylistParams{
		UserID:          userID,
		Uuid:            rec.Uuid,
		OriginalUuid:    rec.OriginalUuid,
		Title:           rec.Title.GetValue(),
		IsDeleted:       rec.IsDeleted.GetValue(),
		AllPodcasts:     boolPtr(rec.AllPodcasts),
		PodcastUuids:    stringPtr(rec.PodcastUuids),
		EpisodeUuids:    stringPtr(rec.EpisodeUuids),
		AudioVideo:      int32Ptr(rec.AudioVideo),
		NotDownloaded:   boolPtr(rec.NotDownloaded),
		Downloaded:      boolPtr(rec.Downloaded),
		Downloading:     boolPtr(rec.Downloading),
		Finished:        boolPtr(rec.Finished),
		PartiallyPlayed: boolPtr(rec.PartiallyPlayed),
		Unplayed:        boolPtr(rec.Unplayed),
		Starred:         boolPtr(rec.Starred),
		Manual:          boolPtr(rec.Manual),
		SortPosition:    int32Ptr(rec.SortPosition),
		SortType:        int32Ptr(rec.SortType),
		IconID:          int32Ptr(rec.IconId),
		FilterHours:     int32Ptr(rec.FilterHours),
		FilterDuration:  boolPtr(rec.FilterDuration),
		LongerThan:      int32Ptr(rec.LongerThan),
		ShorterThan:     int32Ptr(rec.ShorterThan),
		ShowArchived:    boolPtr(rec.ShowArchived),
		// nil (absent) repeated fields must not become SQL NULL — smart and
		// custom playlists carry no episode order at all.
		EpisodeOrder: append([]string{}, rec.EpisodeOrder...),
		Episodes:     episodes,
		CustomQuery:  rec.GetCustomQuery().GetValue(),
		ModifiedAt:   token,
	}
	return q.UpsertPlaylist(ctx, params)
}

func applyFolderRecord(ctx context.Context, q db.Querier, userID int64, token int64, rec *pb.SyncUserFolder) error {
	if rec.FolderUuid == "" {
		return nil
	}

	return q.UpsertFolder(ctx, db.UpsertFolderParams{
		UserID:           userID,
		FolderUuid:       rec.FolderUuid,
		Name:             rec.Name,
		Color:            rec.Color,
		SortPosition:     rec.SortPosition,
		PodcastsSortType: rec.PodcastsSortType,
		DateAdded:        timePtrFromProto(rec.DateAdded),
		IsDeleted:        rec.IsDeleted,
		ModifiedAt:       token,
	})
}

// applyBookmarkRecord honors the per-field modified tokens on title and
// isDeleted; other fields are immutable after creation.
func applyBookmarkRecord(ctx context.Context, q db.Querier, userID int64, token int64, rec *pb.SyncUserBookmark) error {
	if rec.BookmarkUuid == "" {
		return nil
	}

	existing, err := q.GetBookmark(ctx, db.GetBookmarkParams{UserID: userID, BookmarkUuid: rec.BookmarkUuid})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		existing = db.Bookmark{
			UserID:       userID,
			BookmarkUuid: rec.BookmarkUuid,
			PodcastUuid:  rec.PodcastUuid,
			EpisodeUuid:  rec.EpisodeUuid,
			TimeSecs:     rec.Time.GetValue(),
			CreatedAt:    timestampOrNow(rec),
		}
	}

	params := db.UpsertBookmarkParams{
		UserID:            userID,
		BookmarkUuid:      rec.BookmarkUuid,
		PodcastUuid:       existing.PodcastUuid,
		EpisodeUuid:       existing.EpisodeUuid,
		TimeSecs:          existing.TimeSecs,
		Title:             existing.Title,
		TitleModified:     existing.TitleModified,
		CreatedAt:         existing.CreatedAt,
		IsDeleted:         existing.IsDeleted,
		IsDeletedModified: existing.IsDeletedModified,
		ModifiedAt:        token,
	}

	if rec.Title != nil && modifiedAfter(rec.TitleModified, existing.TitleModified) {
		params.Title = rec.Title.Value
		params.TitleModified = rec.TitleModified.Value
	}
	if rec.IsDeleted != nil && modifiedAfter(rec.IsDeletedModified, existing.IsDeletedModified) {
		params.IsDeleted = rec.IsDeleted.Value
		params.IsDeletedModified = rec.IsDeletedModified.Value
	}

	return q.UpsertBookmark(ctx, params)
}

func applyDeviceRecord(ctx context.Context, q db.Querier, userID int64, rec *pb.SyncUserDevice) error {
	deviceID := rec.DeviceId.GetValue()
	if deviceID == "" {
		return nil
	}

	return q.UpsertDevice(ctx, db.UpsertDeviceParams{
		UserID:             userID,
		DeviceID:           deviceID,
		DeviceType:         rec.DeviceType.GetValue(),
		TimesStartedAt:     rec.TimesStartedAt.GetValue(),
		TimeSilenceRemoval: rec.TimeSilenceRemoval.GetValue(),
		TimeVariableSpeed:  rec.TimeVariableSpeed.GetValue(),
		TimeIntroSkipping:  rec.TimeIntroSkipping.GetValue(),
		TimeSkipping:       rec.TimeSkipping.GetValue(),
		TimeListened:       rec.TimeListened.GetValue(),
	})
}

// collectChangedRecords gathers every record with since < modified_at <= upTo
// in the order the client imports them (folders, podcasts, episodes,
// playlists, bookmarks).
func collectChangedRecords(ctx context.Context, q db.Querier, userID int64, since int64, upTo int64) ([]*pb.Record, error) {
	var records []*pb.Record

	folders, err := q.GetFoldersModifiedSince(ctx, db.GetFoldersModifiedSinceParams{UserID: userID, ModifiedAt: since, ModifiedAt_2: upTo})
	if err != nil {
		return nil, err
	}
	for _, row := range folders {
		records = append(records, &pb.Record{Record: &pb.Record_Folder{Folder: folderToProto(row)}})
	}

	podcasts, err := q.GetUserPodcastsModifiedSince(ctx, db.GetUserPodcastsModifiedSinceParams{UserID: userID, ModifiedAt: since, ModifiedAt_2: upTo})
	if err != nil {
		return nil, err
	}
	for _, row := range podcasts {
		records = append(records, &pb.Record{Record: &pb.Record_Podcast{Podcast: userPodcastToProto(row)}})
	}

	episodes, err := q.GetUserEpisodesModifiedSince(ctx, db.GetUserEpisodesModifiedSinceParams{UserID: userID, ModifiedAt: since, ModifiedAt_2: upTo})
	if err != nil {
		return nil, err
	}
	for _, row := range episodes {
		records = append(records, &pb.Record{Record: &pb.Record_Episode{Episode: userEpisodeToProto(row)}})
	}

	playlists, err := q.GetPlaylistsModifiedSince(ctx, db.GetPlaylistsModifiedSinceParams{UserID: userID, ModifiedAt: since, ModifiedAt_2: upTo})
	if err != nil {
		return nil, err
	}
	for _, row := range playlists {
		records = append(records, &pb.Record{Record: &pb.Record_Playlist{Playlist: playlistToProto(row)}})
	}

	bookmarks, err := q.GetBookmarksModifiedSince(ctx, db.GetBookmarksModifiedSinceParams{UserID: userID, ModifiedAt: since, ModifiedAt_2: upTo})
	if err != nil {
		return nil, err
	}
	for _, row := range bookmarks {
		records = append(records, &pb.Record{Record: &pb.Record_Bookmark{Bookmark: bookmarkToProto(row)}})
	}

	return records, nil
}

func timestampOrNow(rec *pb.SyncUserBookmark) time.Time {
	if rec.CreatedAt != nil {
		return rec.CreatedAt.AsTime()
	}
	return time.Now().UTC()
}

func encodePlaylistEpisodes(episodes []*pb.SyncPlaylistEpisode) ([]byte, error) {
	const op errs.Op = "syncsvc/encodePlaylistEpisodes"

	items := make([]json.RawMessage, 0, len(episodes))
	for _, ep := range episodes {
		raw, err := protojson.Marshal(ep)
		if err != nil {
			return nil, errs.E(op, errs.Internal, err)
		}
		items = append(items, raw)
	}

	out, err := json.Marshal(items)
	if err != nil {
		return nil, errs.E(op, errs.Internal, err)
	}
	return out, nil
}

func decodePlaylistEpisodes(raw []byte) []*pb.SyncPlaylistEpisode {
	if len(raw) == 0 {
		return nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}

	episodes := make([]*pb.SyncPlaylistEpisode, 0, len(items))
	for _, item := range items {
		ep := &pb.SyncPlaylistEpisode{}
		if err := protojson.Unmarshal(item, ep); err != nil {
			continue
		}
		episodes = append(episodes, ep)
	}
	return episodes
}
