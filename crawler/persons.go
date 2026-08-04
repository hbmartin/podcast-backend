package crawler

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/jackc/pgx/v5"
	"github.com/mmcdole/gofeed"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// CreditedPerson is one <podcast:person> tag on a feed item.
type CreditedPerson struct {
	Name string
	Role string
	Href string
}

// FoldPersonName is the backend mirror of the iOS `foldedEntityKey` rule
// (ADR-0017): trim -> case fold -> diacritic fold -> collapse inner
// whitespace. The two implementations MUST stay in agreement or the same
// human splits into different identities across the wire.
func FoldPersonName(name string) string {
	stripped, _, err := transform.String(
		transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC),
		name,
	)
	if err != nil {
		stripped = name
	}
	return strings.Join(strings.Fields(strings.ToLower(stripped)), " ")
}

// itemPersons extracts the Podcasting 2.0 <podcast:person> credits.
func itemPersons(item *gofeed.Item) []CreditedPerson {
	var out []CreditedPerson
	for _, tag := range item.Extensions["podcast"]["person"] {
		name := strings.TrimSpace(tag.Value)
		if name == "" {
			continue
		}
		out = append(out, CreditedPerson{
			Name: name,
			Role: strings.ToLower(strings.TrimSpace(tag.Attrs["role"])),
			Href: strings.TrimSpace(tag.Attrs["href"]),
		})
	}
	return out
}

// IngestPersons resolves each credited person to an identity (creating one on
// first sight, v1 heuristic: one person per folded alias) and records the
// appearance. Returns the person ids whose appearance on this episode is NEW —
// the push fan-out set for fresh episodes.
func IngestPersons(ctx context.Context, q db.Querier, podcastUuid, episodeUuid string, persons []CreditedPerson) ([]int64, error) {
	var newAppearances []int64
	for _, credited := range persons {
		folded := FoldPersonName(credited.Name)
		if folded == "" {
			continue
		}

		person, err := q.FindPersonByAlias(ctx, folded)
		if errors.Is(err, pgx.ErrNoRows) {
			person, err = q.CreatePerson(ctx, db.CreatePersonParams{
				CanonicalName: folded,
				DisplayName:   credited.Name,
			})
			if err != nil {
				return newAppearances, err
			}
			if err := q.AddPersonAlias(ctx, db.AddPersonAliasParams{
				PersonID: person.ID, AliasFolded: folded, Source: "ingest",
			}); err != nil {
				return newAppearances, err
			}
		} else if err != nil {
			return newAppearances, err
		}

		// RETURNING only yields a row when the insert actually happened —
		// ON CONFLICT DO NOTHING surfaces as ErrNoRows for repeat appearances.
		_, err = q.UpsertPersonAppearance(ctx, db.UpsertPersonAppearanceParams{
			PersonID:    person.ID,
			PodcastUuid: podcastUuid,
			EpisodeUuid: episodeUuid,
			Role:        credited.Role,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			continue
		case err != nil:
			return newAppearances, err
		default:
			newAppearances = append(newAppearances, person.ID)
		}
	}
	return newAppearances, nil
}
