package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrCorpusQuota = errors.New("corpus contribution quota exceeded")

type CorpusArtifactInput struct {
	Kind        string
	ObjectKey   string
	ContentHash string
	MediaType   string
	Format      string
	ByteLength  int64
	Language    string
	Source      string
	Provenance  []byte
}

type CreateContributionCandidateParams struct {
	EpisodeUuid, PodcastUuid    string
	Language, ContentHash       string
	Attribution, AttributionID  string
	VTTBlob, FingerprintBlob    []byte
	Engine, ModelID, AppVersion string
	Diarized                    bool
	EpisodeDurationSeconds      float64
	CreatedAt                   *time.Time
	AttachmentTokenHash         string
	Artifacts                   []CorpusArtifactInput
	QuotaLimit                  int64
	QuotaSince                  time.Time
}

type CreateContributionCandidateResult struct {
	CandidateID string
}

func (s *pgStore) CreateContributionCandidate(ctx context.Context, arg CreateContributionCandidateParams) (CreateContributionCandidateResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateContributionCandidateResult{}, err
	}
	defer tx.Rollback(ctx)
	lockKey := "contribution:" + arg.Attribution + ":" + arg.AttributionID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return CreateContributionCandidateResult{}, err
	}
	var count int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM transcript_contributions WHERE attribution=$1 AND attribution_id=$2 AND received_at>$3`, arg.Attribution, arg.AttributionID, arg.QuotaSince).Scan(&count); err != nil {
		return CreateContributionCandidateResult{}, err
	}
	if count >= arg.QuotaLimit {
		return CreateContributionCandidateResult{}, ErrCorpusQuota
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "corpus:"+arg.EpisodeUuid+":"+arg.Language+":"+arg.ContentHash); err != nil {
		return CreateContributionCandidateResult{}, err
	}
	var contributionID int64
	var candidateID string
	err = tx.QueryRow(ctx, `
		SELECT id::text,contribution_id FROM corpus_candidates
		WHERE episode_uuid=$1 AND language=$2 AND source='contribution' AND content_hash=$3`,
		arg.EpisodeUuid, arg.Language, arg.ContentHash).Scan(&candidateID, &contributionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CreateContributionCandidateResult{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO transcript_contributions (
				episode_uuid, podcast_uuid, vtt_blob, fingerprint_blob, engine, model_id, language,
				diarized, app_version, episode_duration_seconds, created_at, attribution, attribution_id, content_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id`, arg.EpisodeUuid, arg.PodcastUuid, arg.VTTBlob, arg.FingerprintBlob,
			arg.Engine, arg.ModelID, arg.Language, arg.Diarized, arg.AppVersion, arg.EpisodeDurationSeconds,
			arg.CreatedAt, arg.Attribution, arg.AttributionID, arg.ContentHash).Scan(&contributionID)
		if err != nil {
			return CreateContributionCandidateResult{}, err
		}
		provenance, _ := json.Marshal(map[string]any{"engine": arg.Engine, "model": arg.ModelID, "appVersion": arg.AppVersion, "diarized": arg.Diarized})
		err = tx.QueryRow(ctx, `
			INSERT INTO corpus_candidates (episode_uuid,podcast_uuid,language,source,content_hash,attribution,attribution_id,contribution_id,provenance)
			VALUES ($1,$2,$3,'contribution',$4,$5,$6,$7,$8)
			ON CONFLICT (episode_uuid,language,source,content_hash) WHERE source <> 'legacy'
			DO UPDATE SET content_hash=EXCLUDED.content_hash
			RETURNING id::text`, arg.EpisodeUuid, arg.PodcastUuid, arg.Language, arg.ContentHash,
			arg.Attribution, arg.AttributionID, contributionID, provenance).Scan(&candidateID)
		if err != nil {
			return CreateContributionCandidateResult{}, err
		}
	}
	for _, artifact := range arg.Artifacts {
		provenance := artifact.Provenance
		if len(provenance) == 0 {
			provenance = []byte("{}")
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO corpus_artifacts (candidate_id,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (candidate_id,kind,content_hash) DO NOTHING`, candidateID, artifact.Kind, artifact.ObjectKey,
			artifact.ContentHash, artifact.MediaType, artifact.Format, artifact.ByteLength, artifact.Language, artifact.Source, provenance)
		if err != nil {
			return CreateContributionCandidateResult{}, err
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO corpus_attachment_tokens (candidate_id,token_hash,expires_at)
		VALUES ($1,$2,'infinity'::timestamptz)
		ON CONFLICT (candidate_id) DO UPDATE SET token_hash=EXCLUDED.token_hash, expires_at=EXCLUDED.expires_at, consumed_at=NULL`, candidateID, arg.AttachmentTokenHash)
	if err != nil {
		return CreateContributionCandidateResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,candidate_id,details) VALUES ($1,'candidate_contributed',$2,'{}')`, arg.Attribution+":"+arg.AttributionID, candidateID)
	if err != nil {
		return CreateContributionCandidateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateContributionCandidateResult{}, err
	}
	return CreateContributionCandidateResult{CandidateID: candidateID}, nil
}

type AttachCandidateMetadataParams struct {
	CandidateID string
	TokenHash   string
	Artifacts   []CorpusArtifactInput
}

func (s *pgStore) AttachCandidateMetadata(ctx context.Context, arg AttachCandidateMetadataParams) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var candidateID string
	err = tx.QueryRow(ctx, `
		UPDATE corpus_attachment_tokens SET consumed_at=now()
		WHERE candidate_id=$1 AND token_hash=$2 AND consumed_at IS NULL AND expires_at>now()
		RETURNING candidate_id::text`, arg.CandidateID, arg.TokenHash).Scan(&candidateID)
	if err != nil {
		return err
	}
	for _, artifact := range arg.Artifacts {
		provenance := artifact.Provenance
		if len(provenance) == 0 {
			provenance = []byte("{}")
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO corpus_artifacts (candidate_id,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, candidateID, artifact.Kind, artifact.ObjectKey,
			artifact.ContentHash, artifact.MediaType, artifact.Format, artifact.ByteLength, artifact.Language, artifact.Source, provenance)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,candidate_id,details) VALUES ('attachment-token','metadata_attached',$1,'{}')`, candidateID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type ActiveCorpusRelease struct {
	ID, EpisodeUuid, Language                  string
	Transcript, Fingerprint, Summary, Chapters *CorpusArtifact
}

func scanCorpusArtifact(row pgx.Row) (*CorpusArtifact, error) {
	var artifact CorpusArtifact
	err := row.Scan(&artifact.ID, &artifact.CandidateID, &artifact.Kind, &artifact.ObjectKey, &artifact.ContentHash,
		&artifact.MediaType, &artifact.Format, &artifact.ByteLength, &artifact.Language, &artifact.Source, &artifact.Provenance)
	return &artifact, err
}

func (s *pgStore) GetActiveCorpusRelease(ctx context.Context, episodeUUID string, languages []string) (ActiveCorpusRelease, error) {
	var release ActiveCorpusRelease
	var transcriptID, fingerprintID, summaryID, chaptersID *string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text,episode_uuid,language,transcript_artifact_id::text,fingerprint_artifact_id::text,summary_artifact_id::text,chapters_artifact_id::text
		FROM corpus_releases WHERE episode_uuid=$1 AND active AND language=ANY($2::text[])
		ORDER BY array_position($2::text[],language) LIMIT 1`, episodeUUID, languages).Scan(
		&release.ID, &release.EpisodeUuid, &release.Language, &transcriptID, &fingerprintID, &summaryID, &chaptersID)
	if err != nil {
		return release, err
	}
	load := func(id *string) (*CorpusArtifact, error) {
		if id == nil {
			return nil, nil
		}
		return scanCorpusArtifact(s.pool.QueryRow(ctx, `
			SELECT id::text,candidate_id::text,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance
			FROM corpus_artifacts WHERE id=$1`, *id))
	}
	if release.Transcript, err = load(transcriptID); err != nil {
		return release, err
	}
	if release.Fingerprint, err = load(fingerprintID); err != nil {
		return release, err
	}
	if release.Summary, err = load(summaryID); err != nil {
		return release, err
	}
	if release.Chapters, err = load(chaptersID); err != nil {
		return release, err
	}
	return release, nil
}

func (s *pgStore) GetCorpusArtifact(ctx context.Context, id string) (CorpusArtifact, error) {
	artifact, err := scanCorpusArtifact(s.pool.QueryRow(ctx, `
		SELECT id::text,candidate_id::text,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance
		FROM corpus_artifacts WHERE id=$1`, id))
	if err != nil {
		return CorpusArtifact{}, err
	}
	return *artifact, nil
}

func (s *pgStore) GetEpisodeCorpusLanguage(ctx context.Context, episodeUUID string) (string, error) {
	var language string
	err := s.pool.QueryRow(ctx, `SELECT p.language FROM episodes e JOIN podcasts p ON p.id=e.podcast_id WHERE e.uuid=$1`, episodeUUID).Scan(&language)
	return language, err
}

type CandidateMetadataContext struct {
	Duration float64
	Language string
}

type CreatePublisherCandidateParams struct {
	SightingID                                      int64
	EpisodeUuid, PodcastUuid, Language, ContentHash string
	ObjectKey, MediaType, Format                    string
	ByteLength                                      int64
	TranscriptURL                                   string
}

func (s *pgStore) CreatePublisherCandidate(ctx context.Context, arg CreatePublisherCandidateParams) (string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var verified bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM episodes e, jsonb_array_elements(e.transcripts) item
			WHERE e.uuid::text=$1 AND item->>'url'=$2
		)`, arg.EpisodeUuid, arg.TranscriptURL).Scan(&verified)
	if err != nil {
		return "", false, err
	}
	var candidateID string
	err = tx.QueryRow(ctx, `
		INSERT INTO corpus_candidates (episode_uuid,podcast_uuid,language,source,content_hash,attribution,attribution_id,sighting_id,publisher_verified,provenance)
		VALUES ($1,$2,$3,'publisher',$4,'publisher',$5,$6,$7,jsonb_build_object('url',$5))
		ON CONFLICT (episode_uuid,language,source,content_hash) WHERE source <> 'legacy' DO UPDATE SET publisher_verified=corpus_candidates.publisher_verified OR EXCLUDED.publisher_verified
		RETURNING id::text`, arg.EpisodeUuid, arg.PodcastUuid, arg.Language, arg.ContentHash, arg.TranscriptURL, arg.SightingID, verified).Scan(&candidateID)
	if err != nil {
		return "", false, err
	}
	var artifactID string
	err = tx.QueryRow(ctx, `
		INSERT INTO corpus_artifacts (candidate_id,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance)
		VALUES ($1,'transcript',$2,$3,$4,$5,$6,$7,'publisher',jsonb_build_object('url',$8))
		ON CONFLICT (candidate_id,kind,content_hash) DO UPDATE SET content_hash=EXCLUDED.content_hash
		RETURNING id::text`, candidateID, arg.ObjectKey, arg.ContentHash, arg.MediaType, arg.Format, arg.ByteLength, arg.Language, arg.TranscriptURL).Scan(&artifactID)
	if err != nil {
		return "", false, err
	}
	promoted := false
	if verified {
		var adminOverride bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM corpus_releases WHERE episode_uuid=$1 AND language=$2 AND active AND created_by LIKE 'admin:%')`, arg.EpisodeUuid, arg.Language).Scan(&adminOverride); err != nil {
			return "", false, err
		}
		if !adminOverride {
			if _, err := tx.Exec(ctx, `UPDATE corpus_releases SET active=false WHERE episode_uuid=$1 AND language=$2 AND active`, arg.EpisodeUuid, arg.Language); err != nil {
				return "", false, err
			}
			var releaseID string
			err = tx.QueryRow(ctx, `
				INSERT INTO corpus_releases (episode_uuid,language,transcript_artifact_id,reason,created_by)
				VALUES ($1,$2,$3,'verified publisher URL','system:publisher') RETURNING id::text`, arg.EpisodeUuid, arg.Language, artifactID).Scan(&releaseID)
			if err != nil {
				return "", false, err
			}
			if _, err := tx.Exec(ctx, `UPDATE corpus_candidates SET status='promoted',decided_at=now(),decision_reason='verified publisher URL' WHERE id=$1`, candidateID); err != nil {
				return "", false, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,candidate_id,release_id,details) VALUES ('system:publisher','publisher_auto_promoted',$1,$2,jsonb_build_object('url',$3))`, candidateID, releaseID, arg.TranscriptURL); err != nil {
				return "", false, err
			}
			promoted = true
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return candidateID, promoted, nil
}

type LegacyCorpusRow struct {
	ID                                                                int64
	EpisodeUuid, PodcastUuid                                          string
	VTTBlob, FingerprintBlob                                          []byte
	Engine, ModelID, Language, AppVersion, Attribution, AttributionID string
	Diarized                                                          bool
}

func (s *pgStore) ListLegacyCorpusRows(ctx context.Context, afterID int64, limit int32) ([]LegacyCorpusRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tc.id,tc.episode_uuid,tc.podcast_uuid,tc.vtt_blob,tc.fingerprint_blob,tc.engine,tc.model_id,
		       tc.language,tc.app_version,tc.attribution,tc.attribution_id,tc.diarized
		FROM transcript_contributions tc
		LEFT JOIN corpus_candidates cc ON cc.contribution_id=tc.id
		WHERE tc.id>$1 AND cc.id IS NULL ORDER BY tc.id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []LegacyCorpusRow
	for rows.Next() {
		var item LegacyCorpusRow
		if err := rows.Scan(&item.ID, &item.EpisodeUuid, &item.PodcastUuid, &item.VTTBlob, &item.FingerprintBlob,
			&item.Engine, &item.ModelID, &item.Language, &item.AppVersion, &item.Attribution, &item.AttributionID, &item.Diarized); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type ImportLegacyCorpusCandidateParams struct {
	Row                   LegacyCorpusRow
	Language, ContentHash string
	Artifacts             []CorpusArtifactInput
}

func (s *pgStore) ImportLegacyCorpusCandidate(ctx context.Context, arg ImportLegacyCorpusCandidateParams) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE transcript_contributions SET content_hash=$2 WHERE id=$1 AND content_hash IS NULL`, arg.Row.ID, arg.ContentHash); err != nil {
		return "", err
	}
	provenance, _ := json.Marshal(map[string]any{"legacyContributionId": arg.Row.ID, "engine": arg.Row.Engine, "model": arg.Row.ModelID, "appVersion": arg.Row.AppVersion, "diarized": arg.Row.Diarized})
	var candidateID string
	err = tx.QueryRow(ctx, `
		INSERT INTO corpus_candidates (episode_uuid,podcast_uuid,language,source,content_hash,attribution,attribution_id,contribution_id,provenance)
		VALUES ($1,$2,$3,'legacy',$4,$5,$6,$7,$8)
		RETURNING id::text`, arg.Row.EpisodeUuid, arg.Row.PodcastUuid, arg.Language, arg.ContentHash, arg.Row.Attribution, arg.Row.AttributionID, arg.Row.ID, provenance).Scan(&candidateID)
	if err != nil {
		return "", err
	}
	for _, artifact := range arg.Artifacts {
		if _, err = tx.Exec(ctx, `
			INSERT INTO corpus_artifacts (candidate_id,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'legacy',$9) ON CONFLICT (candidate_id,kind,content_hash) DO NOTHING`,
			candidateID, artifact.Kind, artifact.ObjectKey, artifact.ContentHash, artifact.MediaType, artifact.Format, artifact.ByteLength, arg.Language, provenance); err != nil {
			return "", err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,candidate_id,details) VALUES ('system:migration','legacy_candidate_imported',$1,jsonb_build_object('contributionId',$2))`, candidateID, arg.Row.ID); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return candidateID, nil
}

func (s *pgStore) CandidateMetadataContext(ctx context.Context, candidateID, tokenHash string) (CandidateMetadataContext, error) {
	var result CandidateMetadataContext
	err := s.pool.QueryRow(ctx, `
		SELECT tc.episode_duration_seconds,cc.language FROM corpus_candidates cc
		JOIN transcript_contributions tc ON tc.id=cc.contribution_id
		JOIN corpus_attachment_tokens token ON token.candidate_id=cc.id
		WHERE cc.id=$1 AND token.token_hash=$2 AND token.consumed_at IS NULL AND token.expires_at>now()`, candidateID, tokenHash).Scan(&result.Duration, &result.Language)
	return result, err
}
