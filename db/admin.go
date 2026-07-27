package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

type AdminCandidate struct {
	ID, EpisodeUuid, PodcastUuid, Language, Source, Status, ContentHash string
	Attribution, AttributionID                                          string
	PublisherVerified                                                   bool
	Provenance                                                          []byte
	CreatedAt                                                           string
	Artifacts                                                           []CorpusArtifact
}

type AdminRelease struct {
	ID, EpisodeUuid, Language, Reason, CreatedBy, CreatedAt string
	Active                                                  bool
}

func (s *pgStore) ListAdminReleases(ctx context.Context, limit int32) ([]AdminRelease, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,episode_uuid,language,active,reason,created_by,created_at::text FROM corpus_releases ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AdminRelease
	for rows.Next() {
		var item AdminRelease
		if err := rows.Scan(&item.ID, &item.EpisodeUuid, &item.Language, &item.Active, &item.Reason, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *pgStore) ListAdminCandidates(ctx context.Context, limit int32) ([]AdminCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text,episode_uuid,podcast_uuid,language,source,status,content_hash,attribution,attribution_id,
		       publisher_verified,provenance,created_at::text
		FROM corpus_candidates ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AdminCandidate
	for rows.Next() {
		var item AdminCandidate
		if err := rows.Scan(&item.ID, &item.EpisodeUuid, &item.PodcastUuid, &item.Language, &item.Source, &item.Status, &item.ContentHash,
			&item.Attribution, &item.AttributionID, &item.PublisherVerified, &item.Provenance, &item.CreatedAt); err != nil {
			return nil, err
		}
		artifactRows, err := s.pool.Query(ctx, `SELECT id::text,candidate_id::text,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance FROM corpus_artifacts WHERE candidate_id=$1 ORDER BY kind`, item.ID)
		if err != nil {
			return nil, err
		}
		for artifactRows.Next() {
			artifact, err := scanCorpusArtifact(artifactRows)
			if err != nil {
				artifactRows.Close()
				return nil, err
			}
			item.Artifacts = append(item.Artifacts, *artifact)
		}
		if err := artifactRows.Err(); err != nil {
			artifactRows.Close()
			return nil, err
		}
		artifactRows.Close()
		result = append(result, item)
	}
	return result, rows.Err()
}

type AdminPromoteParams struct {
	EpisodeUuid, Language                                                              string
	TranscriptArtifactID, FingerprintArtifactID, SummaryArtifactID, ChaptersArtifactID *string
	Actor, Reason                                                                      string
}

func (s *pgStore) AdminPromote(ctx context.Context, arg AdminPromoteParams) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	selections := []struct {
		id   *string
		kind string
	}{{arg.TranscriptArtifactID, "transcript"}, {arg.FingerprintArtifactID, "fingerprint"}, {arg.SummaryArtifactID, "summary"}, {arg.ChaptersArtifactID, "chapters"}}
	for _, selection := range selections {
		if selection.id == nil {
			continue
		}
		var kind, episode string
		if err := tx.QueryRow(ctx, `
			SELECT artifact.kind,candidate.episode_uuid
			FROM corpus_artifacts artifact
			JOIN corpus_candidates candidate ON candidate.id=artifact.candidate_id
			WHERE artifact.id=$1`, *selection.id).Scan(&kind, &episode); err != nil {
			return "", err
		}
		if kind != selection.kind {
			return "", errors.New("artifact kind does not match release slot")
		}
		if episode != arg.EpisodeUuid {
			return "", errors.New("artifact belongs to a different episode")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE corpus_releases SET active=false WHERE episode_uuid=$1 AND language=$2 AND active`, arg.EpisodeUuid, arg.Language); err != nil {
		return "", err
	}
	var releaseID string
	err = tx.QueryRow(ctx, `
		INSERT INTO corpus_releases (episode_uuid,language,transcript_artifact_id,fingerprint_artifact_id,summary_artifact_id,chapters_artifact_id,reason,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, arg.EpisodeUuid, arg.Language, arg.TranscriptArtifactID, arg.FingerprintArtifactID, arg.SummaryArtifactID, arg.ChaptersArtifactID, arg.Reason, arg.Actor).Scan(&releaseID)
	if err != nil {
		return "", err
	}
	for _, selection := range selections {
		if selection.id != nil {
			if _, err := tx.Exec(ctx, `UPDATE corpus_candidates SET status='promoted',decided_at=now(),decision_reason=$2 WHERE id=(SELECT candidate_id FROM corpus_artifacts WHERE id=$1)`, *selection.id, arg.Reason); err != nil {
				return "", err
			}
		}
	}
	details, _ := json.Marshal(map[string]any{"language": arg.Language, "reason": arg.Reason})
	if _, err := tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,release_id,details) VALUES ($1,'release_promoted',$2,$3)`, arg.Actor, releaseID, details); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return releaseID, nil
}

func (s *pgStore) AdminRejectCandidate(ctx context.Context, candidateID, actor, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE corpus_candidates SET status='rejected',decision_reason=$2,decided_at=now() WHERE id=$1`, candidateID, reason)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,candidate_id,details) VALUES ($1,'candidate_rejected',$2,jsonb_build_object('reason',$3))`, actor, candidateID, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AdminDerivedCandidateParams struct {
	ParentCandidateID, Language, ContentHash, Actor, Kind, ObjectKey, MediaType, Format string
	ByteLength                                                                          int64
}

func (s *pgStore) AdminCreateDerivedCandidate(ctx context.Context, arg AdminDerivedCandidateParams) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var episode, podcast string
	if err := tx.QueryRow(ctx, `SELECT episode_uuid,podcast_uuid FROM corpus_candidates WHERE id=$1`, arg.ParentCandidateID).Scan(&episode, &podcast); err != nil {
		return "", err
	}
	var candidateID string
	err = tx.QueryRow(ctx, `
		INSERT INTO corpus_candidates (episode_uuid,podcast_uuid,language,source,content_hash,attribution,attribution_id,derived_from,provenance)
		VALUES ($1,$2,$3,'derived',$4,'admin',$5,$6,jsonb_build_object('editedFrom',$6)) RETURNING id::text`, episode, podcast, arg.Language, arg.ContentHash, arg.Actor, arg.ParentCandidateID).Scan(&candidateID)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO corpus_artifacts (candidate_id,kind,object_key,content_hash,media_type,format,byte_length,language,source,provenance)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'derived',jsonb_build_object('editedFrom',$9))`, candidateID, arg.Kind, arg.ObjectKey, arg.ContentHash, arg.MediaType, arg.Format, arg.ByteLength, arg.Language, arg.ParentCandidateID)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,candidate_id,details) VALUES ($1,'derived_candidate_created',$2,jsonb_build_object('parent',$3))`, arg.Actor, candidateID, arg.ParentCandidateID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return candidateID, nil
}

func (s *pgStore) AdminRollbackRelease(ctx context.Context, releaseID, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var episode, language string
	if err := tx.QueryRow(ctx, `SELECT episode_uuid,language FROM corpus_releases WHERE id=$1`, releaseID).Scan(&episode, &language); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE corpus_releases SET active=false WHERE episode_uuid=$1 AND language=$2 AND active`, episode, language); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE corpus_releases SET active=true WHERE id=$1`, releaseID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,release_id,details) VALUES ($1,'release_rollback',$2,'{}')`, actor, releaseID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *pgStore) AdminAudit(ctx context.Context, actor, action string, details []byte) error {
	if len(details) == 0 {
		details = []byte("{}")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO corpus_audit_events(actor,action,details) VALUES ($1,$2,$3)`, actor, action, details)
	return err
}
