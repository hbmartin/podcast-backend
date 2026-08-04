package crawler

import (
	"bytes"
	"testing"

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
