package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/metrics"
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

		notification := Notification{
			Title:      podcast.Title,
			Body:       episode.Title,
			CollapseID: "episode-" + episode.Uuid,
		}
		for _, target := range targets {
			if dropped[target.PushToken] {
				continue
			}
			err := n.Sender.Send(ctx, target.PushToken, target.PushEnvironment, notification)
			if errors.Is(err, ErrUnregistered) {
				metrics.PushDeliveries.WithLabelValues("unregistered").Inc()
				dropped[target.PushToken] = true
				if err := n.DB.ClearPushToken(ctx, target.PushToken); err != nil {
					slog.Warn("push: unable to clear dead token", "error", err)
				}
				continue
			}
			if err != nil {
				metrics.PushDeliveries.WithLabelValues("failed").Inc()
				slog.Warn("push: delivery failed", "podcast", podcastUuid, "error", err)
				continue
			}
			metrics.PushDeliveries.WithLabelValues("delivered").Inc()
		}
	}
}

// SocialPushType mirrors the proto enum (Slice 8). Kept as plain ints here so
// the push package stays proto-free.
const (
	SocialPushFollowRequest  = 1
	SocialPushFollowApproved = 2
	SocialPushNewFollower    = 3
	SocialPushSharedItem     = 4
	SocialPushCommentReply   = 5
	SocialPushListInvite     = 6
	SocialPushGroupInvite    = 7
	SocialPushGroupPost      = 8
	SocialPushDigest         = 9
)

// NotifySocial delivers one social event to every push-enabled device of one
// user (Slice 8, docs/Social.md). Prefs gate at the source: the target's
// social_push_disabled bitmask (bit n = type n+1 off). Best-effort like
// NotifyNewEpisodes; the actor's display name leads the alert body.
func (n *Notifier) NotifySocial(ctx context.Context, targetUserID int64, pushType int, actorHandle, actorDisplayName string, data map[string]string) {
	if pushType < SocialPushFollowRequest || pushType > SocialPushDigest {
		return // corrupt/unknown type: never reach the shift below (QA finding)
	}
	profile, err := n.DB.GetSocialProfileByUserID(ctx, targetUserID)
	if err != nil {
		return // not joined (or gone): nothing to notify
	}
	if profile.SocialPushDisabled&(1<<(pushType-1)) != 0 {
		return // this type is switched off
	}

	targets, err := n.DB.GetPushTargetsForUser(ctx, targetUserID)
	if err != nil || len(targets) == 0 {
		return
	}

	actor := actorDisplayName
	if actor == "" {
		actor = "@" + actorHandle
	}
	var title, body string
	switch pushType {
	case SocialPushFollowRequest:
		title = actor
		body = "wants to follow you"
	case SocialPushFollowApproved:
		title = actor
		body = "approved your follow request"
	case SocialPushNewFollower:
		title = actor
		body = "started following you"
	case SocialPushSharedItem:
		title = actor
		body = "sent you an episode"
		if data["podcast_only"] == "1" {
			body = "recommended a show to you"
		}
	case SocialPushCommentReply:
		title = actor
		body = "replied to your comment"
	case SocialPushListInvite:
		title = actor
		body = "invited you to a shared list"
	case SocialPushGroupInvite:
		title = actor
		body = "invited you to a group"
	case SocialPushGroupPost:
		title = actor
		body = "posted in your group"
	default:
		return
	}

	payloadData := map[string]string{
		"social_type":  strconv.Itoa(pushType),
		"actor_handle": actorHandle,
	}
	for key, value := range data {
		payloadData[key] = value
	}
	notification := Notification{
		Title:      title,
		Body:       body,
		Category:   "so",
		CollapseID: socialCollapseID(pushType, actorHandle, data),
		Data:       payloadData,
	}

	for _, target := range targets {
		if err := n.Sender.Send(ctx, target.PushToken, target.PushEnvironment, notification); err != nil {
			if errors.Is(err, ErrUnregistered) {
				_ = n.DB.ClearPushToken(ctx, target.PushToken)
				continue
			}
			slog.Warn("social push delivery failed", "err", err, "type", pushType)
		}
	}
}

// socialCollapseID is stable for one logical event, including its routing
// data, while keeping APNs' header well below its 64-byte limit. Distinct group
// posts or shared items from the same actor therefore do not collapse together.
func socialCollapseID(pushType int, actorHandle string, data map[string]string) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%d\x00%s\x00", pushType, actorHandle)
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(hash, "%s\x00%s\x00", key, data[key])
	}
	return "social-" + hex.EncodeToString(hash.Sum(nil)[:16])
}

// NotifyDigest delivers the weekly digest (Slice 14): one pre-composed push,
// gated by bitmask bit 9 like every social type, collapse-id "digest" so a
// stale digest never stacks on a fresh one.
func (n *Notifier) NotifyDigest(ctx context.Context, targetUserID int64, title, body string) error {
	profile, err := n.DB.GetSocialProfileByUserID(ctx, targetUserID)
	if err != nil {
		return err
	}
	if profile.SocialPushDisabled&(1<<(SocialPushDigest-1)) != 0 {
		return nil
	}
	targets, err := n.DB.GetPushTargetsForUser(ctx, targetUserID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	var failures []error
	for _, target := range targets {
		notification := Notification{
			Title: title, Body: body,
			Category:   "so",
			CollapseID: "digest",
			Data:       map[string]string{"social_type": "9"},
		}
		if err := n.Sender.Send(ctx, target.PushToken, target.PushEnvironment, notification); err != nil {
			if errors.Is(err, ErrUnregistered) {
				if clearErr := n.DB.ClearPushToken(ctx, target.PushToken); clearErr != nil {
					failures = append(failures, clearErr)
				}
				continue
			}
			slog.Warn("digest push delivery failed", "user", targetUserID, "error", err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// NotifyPersonAppearance fans out to followers of a person newly credited on
// an ingested episode (Highlights B2, ADR-0017). Person follows are private
// account state (no joined profile required), so this deliberately bypasses
// the social-profile gate — the follow itself is the opt-in.
func (n *Notifier) NotifyPersonAppearance(ctx context.Context, personID int64, personName, podcastUuid, episodeUuid string) {
	followers, err := n.DB.GetPersonFollowerIDs(ctx, personID)
	if err != nil || len(followers) == 0 {
		return
	}

	podcast, err := n.DB.GetPodcastByUUID(ctx, podcastUuid)
	if err != nil {
		return
	}
	episode, err := n.DB.GetEpisodeByUUID(ctx, episodeUuid)
	if err != nil {
		return
	}

	notification := Notification{
		Title:      personName,
		Body:       "appears on " + podcast.Title + ": " + episode.Title,
		Category:   "person_appearance",
		// Per-person so two followed credits on one episode don't replace each
		// other. Fits the APNs 64-byte collapse-id cap with nothing to spare
		// (7 + up-to-19-digit id + 1 + 36-char uuid = 63) — don't extend it.
		CollapseID: "person-" + strconv.FormatInt(personID, 10) + "-" + episodeUuid,
		Data: map[string]string{
			"person_id":    strconv.FormatInt(personID, 10),
			"podcast_uuid": podcastUuid,
			"episode_uuid": episodeUuid,
		},
	}

	for _, userID := range followers {
		targets, err := n.DB.GetPushTargetsForUser(ctx, userID)
		if err != nil {
			continue
		}
		for _, target := range targets {
			if err := n.Sender.Send(ctx, target.PushToken, target.PushEnvironment, notification); err != nil {
				slog.Warn("push: person appearance delivery failed", "person", personID, "error", err)
			}
		}
	}
}
