package syncsvc

import (
	"context"

	"goapi-template/db"
	"goapi-template/errs"
	pb "goapi-template/protos/api"
)

// History change actions (SyncHistoryTask.HistoryAction in the client).
const (
	historyActionAdd      = 1
	historyActionDelete   = 2
	historyActionClearAll = 3
)

// maxHistoryItems caps the stored listening history, matching the client's
// ServerConstants.Values.maxHistoryItems.
const maxHistoryItems = 100

// SyncHistory implements POST history/sync.
func (e *Engine) SyncHistory(ctx context.Context, userID int64, req *pb.HistorySyncRequest) (*pb.HistoryResponse, error) {
	const op errs.Op = "syncsvc/Engine.SyncHistory"

	var resp *pb.HistoryResponse
	err := e.DB.InTx(ctx, func(q db.Querier) error {
		user, err := q.GetUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}

		serverModified := user.HistoryModified
		lastCleared := user.HistoryClearedAtMs

		if len(req.Changes) > 0 {
			for _, change := range req.Changes {
				switch change.Action {
				case historyActionAdd:
					if change.Episode == "" {
						continue
					}
					err = q.UpsertHistoryItem(ctx, db.UpsertHistoryItemParams{
						UserID:      userID,
						EpisodeUuid: change.Episode,
						PodcastUuid: change.Podcast,
						Title:       change.Title,
						Url:         change.Url,
						Published:   timePtrFromProto(change.Published),
						ModifiedAt:  change.ModifiedAt,
					})
					if err != nil {
						return err
					}

				case historyActionDelete:
					if change.Episode == "" {
						continue
					}
					if err := q.DeleteHistoryItem(ctx, db.DeleteHistoryItemParams{UserID: userID, EpisodeUuid: change.Episode}); err != nil {
						return err
					}

				case historyActionClearAll:
					clearedAt := change.ModifiedAt
					if clearedAt == 0 {
						clearedAt = NextToken(user.HistoryModified)
					}
					if err := q.DeleteHistoryBefore(ctx, db.DeleteHistoryBeforeParams{UserID: userID, ModifiedAt: clearedAt}); err != nil {
						return err
					}
					lastCleared = clearedAt
				}
			}

			if err := q.TrimHistory(ctx, db.TrimHistoryParams{UserID: userID, Limit: maxHistoryItems}); err != nil {
				return err
			}

			serverModified = NextToken(user.HistoryModified)
			if err := q.SetUserHistoryCleared(ctx, db.SetUserHistoryClearedParams{ID: userID, HistoryClearedAtMs: lastCleared, HistoryModified: serverModified}); err != nil {
				return err
			}
		}

		rows, err := q.GetHistory(ctx, db.GetHistoryParams{UserID: userID, Limit: maxHistoryItems})
		if err != nil {
			return err
		}

		resp = &pb.HistoryResponse{ServerModified: serverModified, LastCleared: lastCleared}
		for _, row := range rows {
			resp.Changes = append(resp.Changes, &pb.HistoryChange{
				Action:     historyActionAdd,
				Episode:    row.EpisodeUuid,
				Podcast:    row.PodcastUuid,
				Title:      row.Title,
				Url:        row.Url,
				Published:  protoFromTimePtr(row.Published),
				ModifiedAt: row.ModifiedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}
	return resp, nil
}
