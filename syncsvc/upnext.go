package syncsvc

import (
	"context"
	"errors"
	"sort"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Up Next change actions (UpNextChanges.Actions in the client).
const (
	upNextActionPlayNow  = 1
	upNextActionPlayNext = 2
	upNextActionPlayLast = 3
	upNextActionRemove   = 4
	upNextActionReplace  = 5
)

// SyncUpNext implements POST up_next/sync: applies the request's queue
// changes (if any) and returns the full server queue.
func (e *Engine) SyncUpNext(ctx context.Context, userID int64, req *pb.UpNextSyncRequest) (*pb.UpNextResponse, error) {
	const op errs.Op = "syncsvc/Engine.SyncUpNext"

	var resp *pb.UpNextResponse
	err := e.DB.InTx(ctx, func(q db.Querier) error {
		user, err := q.GetUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}

		serverModified := user.UpNextModified

		if req.UpNext != nil && len(req.UpNext.Changes) > 0 {
			queue, err := q.GetUpNextItems(ctx, userID)
			if err != nil {
				return err
			}

			// apply changes oldest first so same-batch ordering matches the
			// order the user performed the actions
			changes := append([]*pb.UpNextChanges_Change(nil), req.UpNext.Changes...)
			sort.SliceStable(changes, func(i, j int) bool { return changes[i].Modified < changes[j].Modified })

			for _, change := range changes {
				queue = applyUpNextChange(queue, change, userID)
			}

			if err := rewriteUpNextQueue(ctx, q, userID, queue); err != nil {
				return err
			}

			serverModified = NextToken(user.UpNextModified)
			if err := q.SetUserUpNextModified(ctx, db.SetUserUpNextModifiedParams{ID: userID, UpNextModified: serverModified}); err != nil {
				return err
			}
		}

		queue, err := q.GetUpNextItems(ctx, userID)
		if err != nil {
			return err
		}

		resp = &pb.UpNextResponse{ServerModified: serverModified}
		for _, item := range queue {
			resp.Episodes = append(resp.Episodes, &pb.UpNextResponse_EpisodeResponse{
				Uuid:      item.EpisodeUuid,
				Title:     item.Title,
				Url:       item.Url,
				Podcast:   item.PodcastUuid,
				Published: protoFromTimePtr(item.Published),
			})
		}

		if req.ShowPlayStatus {
			for _, item := range queue {
				episode, err := q.GetUserEpisode(ctx, db.GetUserEpisodeParams{UserID: userID, EpisodeUuid: item.EpisodeUuid})
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						continue
					}
					return err
				}
				resp.EpisodeSync = append(resp.EpisodeSync, &pb.UpNextResponse_EpisodeSyncResponse{
					Uuid:       episode.EpisodeUuid,
					PlayedUpTo: wrapperspb.Int32(int32(episode.PlayedUpTo)),
					Duration:   wrapperspb.Int32(int32(episode.Duration)),
				})
			}
		}

		return nil
	})
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}
	return resp, nil
}

func applyUpNextChange(queue []db.UpNextItem, change *pb.UpNextChanges_Change, userID int64) []db.UpNextItem {
	switch change.Action {
	case upNextActionReplace:
		replacement := make([]db.UpNextItem, 0, len(change.Episodes))
		for _, ep := range change.Episodes {
			if ep.Uuid == "" {
				continue
			}
			replacement = append(replacement, db.UpNextItem{
				UserID:      userID,
				EpisodeUuid: ep.Uuid,
				PodcastUuid: ep.Podcast,
				Title:       ep.Title,
				Url:         ep.Url,
				Published:   timePtrFromProto(ep.Published),
			})
		}
		return replacement

	case upNextActionRemove:
		return removeFromQueue(queue, change.Uuid)

	case upNextActionPlayNow, upNextActionPlayNext, upNextActionPlayLast:
		if change.Uuid == "" {
			return queue
		}
		item := db.UpNextItem{
			UserID:      userID,
			EpisodeUuid: change.Uuid,
			PodcastUuid: change.Podcast,
			Title:       change.Title,
			Url:         change.Url,
			Published:   timePtrFromProto(change.Published),
		}
		queue = removeFromQueue(queue, change.Uuid)

		index := len(queue) // playLast
		switch change.Action {
		case upNextActionPlayNow:
			index = 0
		case upNextActionPlayNext:
			// after the currently playing episode (queue head)
			index = 1
			if len(queue) == 0 {
				index = 0
			}
		}

		queue = append(queue, db.UpNextItem{})
		copy(queue[index+1:], queue[index:])
		queue[index] = item
		return queue

	default:
		return queue
	}
}

func removeFromQueue(queue []db.UpNextItem, uuid string) []db.UpNextItem {
	out := queue[:0]
	for _, item := range queue {
		if item.EpisodeUuid != uuid {
			out = append(out, item)
		}
	}
	return out
}

func rewriteUpNextQueue(ctx context.Context, q db.Querier, userID int64, queue []db.UpNextItem) error {
	if err := q.DeleteAllUpNextItems(ctx, userID); err != nil {
		return err
	}

	for i, item := range queue {
		err := q.InsertUpNextItem(ctx, db.InsertUpNextItemParams{
			UserID:      userID,
			EpisodeUuid: item.EpisodeUuid,
			PodcastUuid: item.PodcastUuid,
			Title:       item.Title,
			Url:         item.Url,
			Published:   item.Published,
			Position:    int32(i),
		})
		if err != nil {
			return err
		}
	}
	return nil
}
