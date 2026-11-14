package app

import (
	"fmt"
	"net/url"
	"testing"

	"git.subcult.tv/subculture-collective/vhness/internal/music"
)

func TestLegacyRecordingFiltersBeforePagination(t *testing.T) {
	entries := map[string]music.Entry{}
	for i := 0; i < 29; i++ {
		id := fmt.Sprintf("tape-%02d", i)
		recording := music.Recording{ID: id, Date: "1977-05-08", Venue: "Barton Hall", Taper: "Fixture taper"}
		if i == 27 {
			recording.Date = "1977"
		}
		if i == 28 {
			recording.Date = ""
		}
		entries[id] = music.Entry{Recording: recording}
	}
	query := url.Values{"q": {"fixture taper"}, "venue": {"barton"}, "date_from": {"1977-05-08"}, "date_to": {"1977-05-08"}, "page": {"2"}}
	items, total, page, err := legacyRecordingPage(entries, query)
	if err != nil || total != 27 || page != 2 || len(items) != 7 || items[0].Recording.ID != "tape-20" {
		t.Fatal("combined audio filters after pagination", items, total, page, err)
	}
	delete(query, "date_from")
	delete(query, "date_to")
	if _, total, _, err := legacyRecordingPage(entries, query); err != nil || total != 29 {
		t.Fatal("unknown source dates should remain without bounds", total, err)
	}
	// Dedhed's rack shows these messages to listeners; they keep its wording.
	for _, invalid := range []struct {
		query url.Values
		want  string
	}{
		{url.Values{"year": {"77"}}, "Use a four-digit year and at most 200 search characters."},
		{url.Values{"date_from": {"1977-02-30"}}, "Use complete YYYY-MM-DD date bounds."},
		{url.Values{"date_from": {"1977-05-09"}, "date_to": {"1977-05-08"}}, "The original-date range is reversed."},
		{url.Values{"page": {"10001"}}, "That page is past the end of the shelf."},
	} {
		if _, _, _, err := legacyRecordingPage(entries, invalid.query); err == nil || err.Error() != invalid.want {
			t.Fatal("invalid audio filter wording", invalid.query, err)
		}
	}
}
