package syncsvc

import (
	"context"
	"errors"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
)

// UpdateSettings implements POST user/named_settings/update: merges changed
// settings (per-key modifiedAt last-writer-wins) and returns everything the
// server has stored.
func (e *Engine) UpdateSettings(ctx context.Context, userID int64, req *pb.NamedSettingsRequest) (*pb.NamedSettingsResponse, error) {
	const op errs.Op = "syncsvc/Engine.UpdateSettings"

	var resp *pb.NamedSettingsResponse
	err := e.DB.InTx(ctx, func(q db.Querier) error {
		user, err := q.GetUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}

		var storedRaw []byte
		row, err := q.GetUserSettings(ctx, userID)
		if err == nil {
			storedRaw = row.Settings
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		stored, err := decodeStoredSettings(storedRaw)
		if err != nil {
			return err
		}

		token := NextToken(user.SyncLastModified)
		changed := MergeChangedSettings(stored, req.ChangedSettings)
		if ApplyLegacySettings(stored, req.Settings, token) {
			changed = true
		}

		if changed {
			raw, err := encodeStoredSettings(stored)
			if err != nil {
				return err
			}
			if err := q.UpsertUserSettings(ctx, db.UpsertUserSettingsParams{UserID: userID, Settings: raw, ModifiedAt: token}); err != nil {
				return err
			}
		}

		resp = BuildNamedSettingsResponse(stored)
		return nil
	})
	if err != nil {
		return nil, errs.E(op, errs.Database, err)
	}
	return resp, nil
}

// UpdateEpisode implements POST sync/update_episode, the real-time playback
// position endpoint. The server token doubles as the per-field modified time.
func (e *Engine) UpdateEpisode(ctx context.Context, userID int64, req *pb.UpdateEpisodeRequest) error {
	const op errs.Op = "syncsvc/Engine.UpdateEpisode"

	if req.Uuid == "" {
		return errs.E(op, errs.Invalid, "episode uuid is required")
	}

	err := e.DB.InTx(ctx, func(q db.Querier) error {
		user, err := q.GetUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		token := NextToken(user.SyncLastModified)

		existing, err := q.GetUserEpisode(ctx, db.GetUserEpisodeParams{UserID: userID, EpisodeUuid: req.Uuid})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			existing = db.UserEpisode{UserID: userID, EpisodeUuid: req.Uuid, PlayingStatus: 1}
		}

		params := upsertParamsFromRow(existing)
		params.ModifiedAt = token
		if req.Podcast != "" {
			params.PodcastUuid = req.Podcast
		}
		if params.PodcastUuid == "" {
			return nil
		}
		if params.PodcastUuid == "" {
			return nil
		}
		if req.Position != nil {
			params.PlayedUpTo = int64(req.Position.Value)
			params.PlayedUpToModified = token
		}
		if req.Status != 0 {
			params.PlayingStatus = req.Status
			params.PlayingStatusModified = token
		}
		if req.Duration != 0 {
			params.Duration = int64(req.Duration)
			params.DurationModified = token
		}

		if err := q.UpsertUserEpisode(ctx, params); err != nil {
			return err
		}
		return q.SetUserSyncLastModified(ctx, db.SetUserSyncLastModifiedParams{ID: userID, SyncLastModified: token})
	})
	if err != nil {
		return errs.E(op, errs.Database, err)
	}
	return nil
}

// UpdateEpisodeStar implements POST sync/update_episode_star.
func (e *Engine) UpdateEpisodeStar(ctx context.Context, userID int64, req *pb.UpdateEpisodeStarRequest) error {
	const op errs.Op = "syncsvc/Engine.UpdateEpisodeStar"

	if req.Uuid == "" {
		return errs.E(op, errs.Invalid, "episode uuid is required")
	}

	err := e.DB.InTx(ctx, func(q db.Querier) error {
		user, err := q.GetUserForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		token := NextToken(user.SyncLastModified)

		existing, err := q.GetUserEpisode(ctx, db.GetUserEpisodeParams{UserID: userID, EpisodeUuid: req.Uuid})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			existing = db.UserEpisode{UserID: userID, EpisodeUuid: req.Uuid, PlayingStatus: 1}
		}

		params := upsertParamsFromRow(existing)
		params.ModifiedAt = token
		if req.Podcast != "" {
			params.PodcastUuid = req.Podcast
		}
		params.Starred = req.Star
		params.StarredModified = token

		if err := q.UpsertUserEpisode(ctx, params); err != nil {
			return err
		}
		return q.SetUserSyncLastModified(ctx, db.SetUserSyncLastModifiedParams{ID: userID, SyncLastModified: token})
	})
	if err != nil {
		return errs.E(op, errs.Database, err)
	}
	return nil
}

func upsertParamsFromRow(row db.UserEpisode) db.UpsertUserEpisodeParams {
	return db.UpsertUserEpisodeParams(row)
}
