package transcripts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/objectstore"
	"golang.org/x/text/language"
)

type publisherCorpusStore interface {
	CreatePublisherCandidate(context.Context, db.CreatePublisherCandidateParams) (string, bool, error)
}

func PromoteFetchedSighting(ctx context.Context, store db.Store, objects objectstore.Store, sightingID int64) error {
	if objects == nil {
		return nil
	}
	repository, ok := store.(publisherCorpusStore)
	if !ok {
		return nil
	}
	sighting, err := store.GetTranscriptSighting(ctx, sightingID)
	if err != nil {
		return err
	}
	if sighting.Status != "fetched" || len(sighting.Content) == 0 {
		return nil
	}
	normalized, err := NormalizePublisherContent(sighting.Content, sighting.ContentType)
	if err != nil {
		slog.Warn("Publisher transcript normalization rejected", "sighting_id", sightingID, "error", err)
		return store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "rejected"})
	}
	hash := sha256.Sum256(normalized.Body)
	hashString := hex.EncodeToString(hash[:])
	extension := normalized.Format
	if extension == "plain" {
		extension = "txt"
	}
	objectKey := "corpus/publisher/" + hashString + "." + extension
	if err := objects.Put(ctx, objectKey, normalized.Body, normalized.MediaType); err != nil {
		return err
	}
	languageValue := "und"
	if value := strings.TrimSpace(sighting.Language); value != "" {
		if tag, parseErr := language.Parse(value); parseErr == nil {
			languageValue = tag.String()
		}
	}
	candidateID, promoted, err := repository.CreatePublisherCandidate(ctx, db.CreatePublisherCandidateParams{
		SightingID: sighting.ID, EpisodeUuid: sighting.EpisodeUuid, PodcastUuid: sighting.PodcastUuid,
		Language: languageValue, ContentHash: hashString, ObjectKey: objectKey, MediaType: normalized.MediaType,
		Format: normalized.Format, ByteLength: int64(len(normalized.Body)), TranscriptURL: sighting.TranscriptUrl,
	})
	if err != nil {
		return err
	}
	slog.Info("Publisher transcript candidate stored", "candidate_id", candidateID, "promoted", promoted)
	return nil
}
