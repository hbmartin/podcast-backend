package crawler

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mmcdole/gofeed"
)

// FoldPersonName must stay byte-identical with the iOS `foldedEntityKey`
// rule (ADR-0017) — these cases mirror the iOS test suite.
func TestFoldPersonName(t *testing.T) {
	cases := map[string]string{
		"  Ada  Lovelace ": "ada lovelace",
		"ADA LOVELACE":     "ada lovelace",
		"José Piñera":      "jose pinera",
		"":                 "",
	}
	for input, want := range cases {
		if got := FoldPersonName(input); got != want {
			t.Errorf("FoldPersonName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestItemPersonsExtractsPodcastPersonTags(t *testing.T) {
	feedXML := `<?xml version="1.0"?>
	<rss version="2.0" xmlns:podcast="https://podcastindex.org/namespace/1.0">
	  <channel><title>Show</title>
	    <item>
	      <title>Ep</title><guid>g1</guid>
	      <enclosure url="https://x/audio.mp3" type="audio/mpeg" length="1"/>
	      <podcast:person role="Host" href="https://wikidata.org/wiki/Q7259">Ada Lovelace</podcast:person>
	      <podcast:person>Grace Hopper</podcast:person>
	      <podcast:person></podcast:person>
	    </item>
	  </channel>
	</rss>`

	feed, err := gofeed.NewParser().Parse(bytes.NewReader([]byte(feedXML)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	persons := itemPersons(feed.Items[0])

	if len(persons) != 2 {
		t.Fatalf("want 2 persons, got %d: %+v", len(persons), persons)
	}
	if persons[0].Name != "Ada Lovelace" || persons[0].Role != "host" || persons[0].Href == "" {
		t.Errorf("first person mis-parsed: %+v", persons[0])
	}
	if persons[1].Name != "Grace Hopper" || persons[1].Role != "" {
		t.Errorf("second person mis-parsed: %+v", persons[1])
	}
}

// personStoreFake is an in-memory db.Store covering the person-ingest
// queries, including the 036 partial unique index on ingest aliases and
// transaction rollback.
type personStoreFake struct {
	db.Store

	nextID      int64
	persons     map[int64]db.Person
	aliasOwner  map[string]int64 // folded alias -> owning person (unique for ingest)
	refs        map[string]int64 // scheme+"\x00"+value -> person
	appearances map[string]bool  // personID + "|" + episodeUuid
	findMisses  int              // initial FindPersonByAlias calls that miss, simulating a not-yet-seen winner
}

func newPersonStoreFake() *personStoreFake {
	return &personStoreFake{
		persons:     map[int64]db.Person{},
		aliasOwner:  map[string]int64{},
		refs:        map[string]int64{},
		appearances: map[string]bool{},
	}
}

func (f *personStoreFake) InTx(ctx context.Context, fn func(q db.Querier) error) error {
	personsSnapshot := map[int64]db.Person{}
	for id, p := range f.persons {
		personsSnapshot[id] = p
	}
	aliasSnapshot := map[string]int64{}
	for alias, id := range f.aliasOwner {
		aliasSnapshot[alias] = id
	}
	if err := fn(f); err != nil {
		f.persons = personsSnapshot
		f.aliasOwner = aliasSnapshot
		return err
	}
	return nil
}

func (f *personStoreFake) FindPersonByAlias(ctx context.Context, aliasFolded string) (db.Person, error) {
	if f.findMisses > 0 {
		f.findMisses--
		return db.Person{}, pgx.ErrNoRows
	}
	if id, ok := f.aliasOwner[aliasFolded]; ok {
		return f.persons[id], nil
	}
	return db.Person{}, pgx.ErrNoRows
}

func (f *personStoreFake) CreatePerson(ctx context.Context, arg db.CreatePersonParams) (db.Person, error) {
	f.nextID++
	person := db.Person{ID: f.nextID, CanonicalName: arg.CanonicalName, DisplayName: arg.DisplayName}
	f.persons[person.ID] = person
	return person, nil
}

func (f *personStoreFake) AddPersonAlias(ctx context.Context, arg db.AddPersonAliasParams) error {
	if owner, ok := f.aliasOwner[arg.AliasFolded]; ok && owner != arg.PersonID {
		return &pgconn.PgError{Code: "23505"}
	}
	f.aliasOwner[arg.AliasFolded] = arg.PersonID
	return nil
}

func (f *personStoreFake) AddPersonExternalRef(ctx context.Context, arg db.AddPersonExternalRefParams) error {
	key := arg.Scheme + "\x00" + arg.Value
	if _, ok := f.refs[key]; !ok {
		f.refs[key] = arg.PersonID
	}
	return nil
}

func (f *personStoreFake) UpsertPersonAppearance(ctx context.Context, arg db.UpsertPersonAppearanceParams) (int64, error) {
	key := strconv.FormatInt(arg.PersonID, 10) + "|" + arg.EpisodeUuid
	if f.appearances[key] {
		return 0, pgx.ErrNoRows
	}
	f.appearances[key] = true
	return arg.PersonID, nil
}

func TestIngestPersonsCreatesIdentityWithExternalRef(t *testing.T) {
	store := newPersonStoreFake()
	ctx := context.Background()

	credited := []CreditedPerson{{Name: "Ada Lovelace", Role: "host", Href: "https://wikidata.org/wiki/Q7259"}}
	newIDs, err := IngestPersons(ctx, store, "pod-1", "ep-1", credited)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(newIDs) != 1 {
		t.Fatalf("want 1 new appearance, got %v", newIDs)
	}

	person := store.persons[newIDs[0]]
	if person.CanonicalName != "ada lovelace" || person.DisplayName != "Ada Lovelace" {
		t.Errorf("person mis-created: %+v", person)
	}
	if store.aliasOwner["ada lovelace"] != person.ID {
		t.Errorf("alias not owned by created person")
	}
	if store.refs["href\x00https://wikidata.org/wiki/Q7259"] != person.ID {
		t.Errorf("external ref not persisted: %+v", store.refs)
	}

	// Repeat ingest of the same episode: same identity, no new appearance.
	newIDs, err = IngestPersons(ctx, store, "pod-1", "ep-1", credited)
	if err != nil {
		t.Fatalf("repeat ingest: %v", err)
	}
	if len(newIDs) != 0 {
		t.Errorf("repeat appearance reported as new: %v", newIDs)
	}
	if len(store.persons) != 1 {
		t.Errorf("duplicate person created: %+v", store.persons)
	}
}

func TestIngestPersonsRaceLoserAdoptsWinner(t *testing.T) {
	store := newPersonStoreFake()
	ctx := context.Background()

	// Another crawl already committed the identity, but this crawl's initial
	// lookup ran before that commit was visible.
	winner, _ := store.CreatePerson(ctx, db.CreatePersonParams{CanonicalName: "ada lovelace", DisplayName: "Ada Lovelace"})
	store.aliasOwner["ada lovelace"] = winner.ID
	store.findMisses = 1

	newIDs, err := IngestPersons(ctx, store, "pod-1", "ep-1", []CreditedPerson{{Name: "Ada Lovelace"}})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(newIDs) != 1 || newIDs[0] != winner.ID {
		t.Fatalf("appearance not attributed to race winner: %v", newIDs)
	}
	if len(store.persons) != 1 {
		t.Errorf("loser's person row not rolled back: %+v", store.persons)
	}
}

func TestIngestPersonsSkipsOversizeHref(t *testing.T) {
	store := newPersonStoreFake()

	href := "https://example.com/" + strings.Repeat("x", maxPersonHrefLen)
	_, err := IngestPersons(context.Background(), store, "pod-1", "ep-1",
		[]CreditedPerson{{Name: "Ada Lovelace", Href: href}})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(store.refs) != 0 {
		t.Errorf("oversize href persisted: %+v", store.refs)
	}
}
