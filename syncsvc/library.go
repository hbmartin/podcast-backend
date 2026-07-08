package syncsvc

import (
	"context"
	"errors"
	"time"

	"goapi-template/db"
	"goapi-template/errs"
	pb "goapi-template/protos/api"

	"github.com/jackc/pgx/v5"
)

// PodcastList implements POST user/podcast/list (full-sync home grid):
// subscribed podcasts joined with catalog metadata, plus folders.
func (e *Engine) PodcastList(ctx context.Context, userID int64) (*pb.UserPodcastListResponse, error) {
	const op errs.Op = "syncsvc/Engine.PodcastList"

	rows, err := e.DB.GetSubscribedPodcastsWithCatalog(ctx, userID)
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}

	resp := &pb.UserPodcastListResponse{}
	for _, row := range rows {
		podcast := &pb.UserPodcastResponse{
			Uuid:         row.PodcastUuid,
			Title:        row.CatTitle,
			Author:       row.CatAuthor,
			Description:  row.CatDescription,
			Url:          row.CatWebsiteUrl,
			FolderUuid:   wrapString(row.FolderUuid),
			SortPosition: wrapInt32(row.SortPosition),
			DateAdded:    protoFromTimePtr(row.DateAdded),
			Settings:     decodePodcastSettings(row.Settings),
		}
		if row.AutoStartFrom != nil {
			podcast.AutoStartFrom = *row.AutoStartFrom
		}
		if row.AutoSkipLast != nil {
			podcast.AutoSkipLast = *row.AutoSkipLast
		}
		if row.EpisodesSortOrder != nil {
			podcast.EpisodesSortOrder = *row.EpisodesSortOrder
		}
		if row.CatLatestEpisodeUuid != nil {
			podcast.LastEpisodeUuid = *row.CatLatestEpisodeUuid
		}
		podcast.LastEpisodePublished = protoFromTimePtr(row.CatLatestEpisodePublished)
		resp.Podcasts = append(resp.Podcasts, podcast)
	}

	folders, err := e.DB.GetFolders(ctx, userID)
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}
	for _, row := range folders {
		resp.Folders = append(resp.Folders, &pb.PodcastFolder{
			FolderUuid:       row.FolderUuid,
			Name:             row.Name,
			Color:            row.Color,
			SortPosition:     row.SortPosition,
			PodcastsSortType: row.PodcastsSortType,
			DateAdded:        protoFromTimePtr(row.DateAdded),
		})
	}

	return resp, nil
}

// PodcastEpisodes implements POST user/podcast/episodes: the user's sync
// state for every episode of one podcast, bookmarks folded in when requested.
func (e *Engine) PodcastEpisodes(ctx context.Context, userID int64, req *pb.UuidRequest) (*pb.SyncEpisodesResponse, error) {
	const op errs.Op = "syncsvc/Engine.PodcastEpisodes"

	if req.Uuid == "" {
		return nil, errs.E(op, errs.Invalid, "podcast uuid is required")
	}

	episodes, err := e.DB.GetUserEpisodesForPodcast(ctx, db.GetUserEpisodesForPodcastParams{UserID: userID, PodcastUuid: req.Uuid})
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}

	bookmarksByEpisode := map[string][]*pb.BookmarkResponse{}
	if req.IncludeBookmarks && len(episodes) > 0 {
		uuids := make([]string, 0, len(episodes))
		for _, ep := range episodes {
			uuids = append(uuids, ep.EpisodeUuid)
		}
		bookmarks, err := e.DB.GetBookmarksForEpisodes(ctx, db.GetBookmarksForEpisodesParams{UserID: userID, Column2: uuids})
		if err != nil {
			return nil, errs.E(op, errs.Database, err)
		}
		for _, bm := range bookmarks {
			bookmarksByEpisode[bm.EpisodeUuid] = append(bookmarksByEpisode[bm.EpisodeUuid], bookmarkToResponse(bm))
		}
	}

	resp := &pb.SyncEpisodesResponse{}
	for _, ep := range episodes {
		resp.Episodes = append(resp.Episodes, &pb.EpisodeSyncResponse{
			Uuid:               ep.EpisodeUuid,
			PlayingStatus:      ep.PlayingStatus,
			PlayedUpTo:         int32(ep.PlayedUpTo),
			IsDeleted:          ep.IsDeleted,
			Starred:            ep.Starred,
			Duration:           int32(ep.Duration),
			Bookmarks:          bookmarksByEpisode[ep.EpisodeUuid],
			DeselectedChapters: ep.DeselectedChapters,
		})
	}

	sub, err := e.DB.GetUserPodcast(ctx, db.GetUserPodcastParams{UserID: userID, PodcastUuid: req.Uuid})
	if err == nil {
		resp.AutoStartFrom = wrapInt32(sub.AutoStartFrom)
		resp.AutoSkipLast = wrapInt32(sub.AutoSkipLast)
		resp.EpisodesSortOrder = wrapInt32(sub.EpisodesSortOrder)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, errs.E(op, errs.Database, err)
	}

	return resp, nil
}

// PlaylistList implements POST user/playlist/list.
func (e *Engine) PlaylistList(ctx context.Context, userID int64) (*pb.UserPlaylistListResponse, error) {
	const op errs.Op = "syncsvc/Engine.PlaylistList"

	rows, err := e.DB.GetPlaylists(ctx, userID)
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}

	resp := &pb.UserPlaylistListResponse{}
	for _, row := range rows {
		resp.Playlists = append(resp.Playlists, playlistToSyncResponse(row))
	}
	return resp, nil
}

// BookmarkList implements POST user/bookmark/list.
func (e *Engine) BookmarkList(ctx context.Context, userID int64) (*pb.BookmarksResponse, error) {
	const op errs.Op = "syncsvc/Engine.BookmarkList"

	rows, err := e.DB.GetBookmarks(ctx, userID)
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}

	resp := &pb.BookmarksResponse{}
	for _, row := range rows {
		resp.Bookmarks = append(resp.Bookmarks, bookmarkToResponse(row))
	}
	return resp, nil
}

// StarredList implements POST starred/list.
func (e *Engine) StarredList(ctx context.Context, userID int64) (*pb.StarredEpisodesResponse, error) {
	const op errs.Op = "syncsvc/Engine.StarredList"

	rows, err := e.DB.GetStarredEpisodes(ctx, userID)
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}

	resp := &pb.StarredEpisodesResponse{}
	for _, row := range rows {
		resp.Episodes = append(resp.Episodes, &pb.StarredEpisode{
			Uuid:            row.EpisodeUuid,
			PodcastUuid:     row.PodcastUuid,
			Duration:        int32(row.Duration),
			PlayingStatus:   row.PlayingStatus,
			PlayedUpTo:      int32(row.PlayedUpTo),
			IsDeleted:       row.IsDeleted,
			StarredModified: row.StarredModified,
		})
	}
	return resp, nil
}

// LastSyncAt implements POST user/last_sync_at.
func (e *Engine) LastSyncAt(ctx context.Context, userID int64) (*pb.UserLastSyncAtResponse, error) {
	const op errs.Op = "syncsvc/Engine.LastSyncAt"

	user, err := e.DB.GetUserByID(ctx, userID)
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}

	resp := &pb.UserLastSyncAtResponse{LastSyncAtMs: user.SyncLastModified}
	if user.SyncLastModified > 0 {
		resp.LastSyncAt = time.UnixMilli(user.SyncLastModified).UTC().Format(time.RFC3339)
	}
	return resp, nil
}
