package syncsvc

import (
	"time"

	"goapi-template/db"
	pb "goapi-template/protos/api"

	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func timestampFromMillis(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

func timePtrFromProto(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}

func protoFromTimePtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func int32Ptr(v *wrapperspb.Int32Value) *int32 {
	if v == nil {
		return nil
	}
	val := v.Value
	return &val
}

func boolPtr(v *wrapperspb.BoolValue) *bool {
	if v == nil {
		return nil
	}
	val := v.Value
	return &val
}

func stringPtr(v *wrapperspb.StringValue) *string {
	if v == nil {
		return nil
	}
	val := v.Value
	return &val
}

func wrapInt32(v *int32) *wrapperspb.Int32Value {
	if v == nil {
		return nil
	}
	return wrapperspb.Int32(*v)
}

func wrapBool(v *bool) *wrapperspb.BoolValue {
	if v == nil {
		return nil
	}
	return wrapperspb.Bool(*v)
}

func wrapString(v *string) *wrapperspb.StringValue {
	if v == nil {
		return nil
	}
	return wrapperspb.String(*v)
}

// userPodcastToProto converts a stored subscription row to the sync record
// echoed to devices.
func userPodcastToProto(row db.UserPodcast) *pb.SyncUserPodcast {
	return &pb.SyncUserPodcast{
		Uuid:              row.PodcastUuid,
		IsDeleted:         wrapperspb.Bool(row.IsDeleted),
		Subscribed:        wrapperspb.Bool(row.Subscribed),
		AutoStartFrom:     wrapInt32(row.AutoStartFrom),
		EpisodesSortOrder: wrapInt32(row.EpisodesSortOrder),
		AutoSkipLast:      wrapInt32(row.AutoSkipLast),
		FolderUuid:        wrapString(row.FolderUuid),
		SortPosition:      wrapInt32(row.SortPosition),
		DateAdded:         protoFromTimePtr(row.DateAdded),
		Settings:          decodePodcastSettings(row.Settings),
	}
}

func userEpisodeToProto(row db.UserEpisode) *pb.SyncUserEpisode {
	return &pb.SyncUserEpisode{
		Uuid:                       row.EpisodeUuid,
		PodcastUuid:                row.PodcastUuid,
		IsDeleted:                  wrapperspb.Bool(row.IsDeleted),
		IsDeletedModified:          wrapperspb.Int64(row.IsDeletedModified),
		Duration:                   wrapperspb.Int64(row.Duration),
		DurationModified:           wrapperspb.Int64(row.DurationModified),
		PlayingStatus:              wrapperspb.Int32(row.PlayingStatus),
		PlayingStatusModified:      wrapperspb.Int64(row.PlayingStatusModified),
		PlayedUpTo:                 wrapperspb.Int64(row.PlayedUpTo),
		PlayedUpToModified:         wrapperspb.Int64(row.PlayedUpToModified),
		Starred:                    wrapperspb.Bool(row.Starred),
		StarredModified:            wrapperspb.Int64(row.StarredModified),
		DeselectedChapters:         row.DeselectedChapters,
		DeselectedChaptersModified: wrapperspb.Int64(row.DeselectedChaptersModified),
	}
}

func folderToProto(row db.Folder) *pb.SyncUserFolder {
	return &pb.SyncUserFolder{
		FolderUuid:       row.FolderUuid,
		IsDeleted:        row.IsDeleted,
		Name:             row.Name,
		Color:            row.Color,
		SortPosition:     row.SortPosition,
		PodcastsSortType: row.PodcastsSortType,
		DateAdded:        protoFromTimePtr(row.DateAdded),
	}
}

func playlistToProto(row db.Playlist) *pb.SyncUserPlaylist {
	return &pb.SyncUserPlaylist{
		Uuid:            row.Uuid,
		OriginalUuid:    row.OriginalUuid,
		IsDeleted:       wrapperspb.Bool(row.IsDeleted),
		Title:           wrapString(&row.Title),
		AllPodcasts:     wrapBool(row.AllPodcasts),
		PodcastUuids:    wrapString(row.PodcastUuids),
		EpisodeUuids:    wrapString(row.EpisodeUuids),
		AudioVideo:      wrapInt32(row.AudioVideo),
		NotDownloaded:   wrapBool(row.NotDownloaded),
		Downloaded:      wrapBool(row.Downloaded),
		Downloading:     wrapBool(row.Downloading),
		Finished:        wrapBool(row.Finished),
		PartiallyPlayed: wrapBool(row.PartiallyPlayed),
		Unplayed:        wrapBool(row.Unplayed),
		Starred:         wrapBool(row.Starred),
		Manual:          wrapBool(row.Manual),
		SortPosition:    wrapInt32(row.SortPosition),
		SortType:        wrapInt32(row.SortType),
		IconId:          wrapInt32(row.IconID),
		FilterHours:     wrapInt32(row.FilterHours),
		FilterDuration:  wrapBool(row.FilterDuration),
		LongerThan:      wrapInt32(row.LongerThan),
		ShorterThan:     wrapInt32(row.ShorterThan),
		ShowArchived:    wrapBool(row.ShowArchived),
		EpisodeOrder:    row.EpisodeOrder,
		Episodes:        decodePlaylistEpisodes(row.Episodes),
	}
}

func playlistToSyncResponse(row db.Playlist) *pb.PlaylistSyncResponse {
	return &pb.PlaylistSyncResponse{
		Uuid:            row.Uuid,
		OriginalUuid:    row.OriginalUuid,
		IsDeleted:       wrapperspb.Bool(row.IsDeleted),
		Title:           row.Title,
		AllPodcasts:     wrapBool(row.AllPodcasts),
		PodcastUuids:    orEmpty(row.PodcastUuids),
		EpisodeUuids:    orEmpty(row.EpisodeUuids),
		AudioVideo:      wrapInt32(row.AudioVideo),
		NotDownloaded:   wrapBool(row.NotDownloaded),
		Downloaded:      wrapBool(row.Downloaded),
		Downloading:     wrapBool(row.Downloading),
		Finished:        wrapBool(row.Finished),
		PartiallyPlayed: wrapBool(row.PartiallyPlayed),
		Unplayed:        wrapBool(row.Unplayed),
		Starred:         wrapBool(row.Starred),
		Manual:          wrapBool(row.Manual),
		SortPosition:    wrapInt32(row.SortPosition),
		SortType:        wrapInt32(row.SortType),
		IconId:          wrapInt32(row.IconID),
		FilterHours:     wrapInt32(row.FilterHours),
		FilterDuration:  wrapBool(row.FilterDuration),
		LongerThan:      wrapInt32(row.LongerThan),
		ShorterThan:     wrapInt32(row.ShorterThan),
		ShowArchived:    wrapBool(row.ShowArchived),
		EpisodeOrder:    row.EpisodeOrder,
		Episodes:        decodePlaylistEpisodes(row.Episodes),
	}
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func bookmarkToProto(row db.Bookmark) *pb.SyncUserBookmark {
	return &pb.SyncUserBookmark{
		BookmarkUuid:      row.BookmarkUuid,
		PodcastUuid:       row.PodcastUuid,
		EpisodeUuid:       row.EpisodeUuid,
		CreatedAt:         timestamppb.New(row.CreatedAt),
		Time:              wrapperspb.Int32(row.TimeSecs),
		Title:             wrapperspb.String(row.Title),
		TitleModified:     wrapperspb.Int64(row.TitleModified),
		IsDeleted:         wrapperspb.Bool(row.IsDeleted),
		IsDeletedModified: wrapperspb.Int64(row.IsDeletedModified),
	}
}

func bookmarkToResponse(row db.Bookmark) *pb.BookmarkResponse {
	return &pb.BookmarkResponse{
		BookmarkUuid: row.BookmarkUuid,
		PodcastUuid:  row.PodcastUuid,
		EpisodeUuid:  row.EpisodeUuid,
		Time:         row.TimeSecs,
		Title:        row.Title,
		CreatedAt:    timestamppb.New(row.CreatedAt),
	}
}
