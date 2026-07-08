package push

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hbmartin/podcast-backend/db"
)

// maxEpisodesPerNotify caps alerts per crawl so a feed that suddenly
// backfills its archive doesn't storm every subscriber.
const maxEpisodesPerNotify = 3

// Notifier fans newly published episodes out to every device that has push
// enabled for the podcast. Delivery is best-effort: individual failures are
// logged, unregistered tokens are dropped.
type Notifier struct {
	DB     db.Store
	Sender Sender
}

// NotifyNewEpisodes sends one alert per new episode (newest-first input,
// capped) to each registered device.
func (n *Notifier) NotifyNewEpisodes(ctx context.Context, podcastUuid string, episodeUuids []string) {
	if len(episodeUuids) == 0 {
		return
	}

	targets, err := n.DB.GetPushTargetsForPodcast(ctx, podcastUuid)
	if err != nil {
		slog.Warn("push: unable to resolve targets", "podcast", podcastUuid, "error", err)
		return
	}
	if len(targets) == 0 {
		return
	}

	podcast, err := n.DB.GetPodcastByUUID(ctx, podcastUuid)
	if err != nil {
		slog.Warn("push: unable to load podcast", "podcast", podcastUuid, "error", err)
		return
	}

	if len(episodeUuids) > maxEpisodesPerNotify {
		episodeUuids = episodeUuids[:maxEpisodesPerNotify]
	}

	dropped := map[string]bool{}
	for _, episodeUuid := range episodeUuids {
		episode, err := n.DB.GetEpisodeByUUID(ctx, episodeUuid)
		if err != nil {
			slog.Warn("push: unable to load episode", "episode", episodeUuid, "error", err)
			continue
		}

		notification := Notification{Title: podcast.Title, Body: episode.Title}
		for _, target := range targets {
			if dropped[target.PushToken] {
				continue
			}
			err := n.Sender.Send(ctx, target.PushToken, notification)
			if errors.Is(err, ErrUnregistered) {
				dropped[target.PushToken] = true
				if err := n.DB.ClearPushToken(ctx, target.PushToken); err != nil {
					slog.Warn("push: unable to clear dead token", "error", err)
				}
				continue
			}
			if err != nil {
				slog.Warn("push: delivery failed", "podcast", podcastUuid, "error", err)
			}
		}
	}
}
