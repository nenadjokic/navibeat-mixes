package library

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

// fakeServer answers the four endpoints Candidates uses, and counts what was
// asked for. Counting is the point of half these tests: the cost of a run is
// measured in getAlbum calls, and a duplicate one is invisible in the result.
type fakeServer struct {
	starred []string            // song ids
	lists   map[string][]string // album list type -> album ids
	albums  map[string][]string // album id -> song ids
	calls   map[string]int      // endpoint -> times called
	albumHi map[string]int      // album id -> times fetched
	// meta holds album-level fields (year, releaseDate, originalReleaseDate)
	// in the exact JSON shape a 0.63.2 getAlbum answers with; absent means
	// the album carries no date at all, like an untagged library.
	meta map[string]map[string]any
	// songYear is the song-level `year`, the only date a starred song gets.
	songYear map[string]int
	// listParams records the query of every getAlbumList2 call, so a test
	// can assert which lists were asked for and with what window.
	listParams []url.Values
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		lists:    map[string][]string{},
		albums:   map[string][]string{},
		calls:    map[string]int{},
		albumHi:  map[string]int{},
		meta:     map[string]map[string]any{},
		songYear: map[string]int{},
	}
}

// listsOfType is every getAlbumList2 call made for one list type.
func (f *fakeServer) listsOfType(t string) []url.Values {
	var out []url.Values
	for _, p := range f.listParams {
		if p.Get("type") == t {
			out = append(out, p)
		}
	}
	return out
}

func (f *fakeServer) call(uri string) (string, error) {
	endpoint, query, _ := strings.Cut(uri, "?")
	params, err := url.ParseQuery(query)
	if err != nil {
		return "", err
	}
	f.calls[endpoint]++

	type child struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Artist  string `json:"artist"`
		Year    int    `json:"year,omitempty"`
		Created string `json:"created,omitempty"`
		Starred string `json:"starred,omitempty"`
	}
	body := map[string]any{"status": "ok"}

	switch endpoint {
	case "getStarred2":
		songs := make([]child, 0, len(f.starred))
		for _, id := range f.starred {
			songs = append(songs, child{ID: id, Title: id, Artist: "Artist " + id, Year: f.songYear[id],
				Starred: "2026-08-01T00:00:00Z"})
		}
		body["starred2"] = map[string]any{"song": songs}
	case "getAlbumList2":
		f.listParams = append(f.listParams, params)
		albums := make([]map[string]string, 0)
		for _, id := range f.lists[params.Get("type")] {
			albums = append(albums, map[string]string{"id": id})
		}
		body["albumList2"] = map[string]any{"album": albums}
	case "getAlbum":
		id := params.Get("id")
		f.albumHi[id]++
		songs := make([]child, 0)
		for _, s := range f.albums[id] {
			songs = append(songs, child{ID: s, Title: s, Artist: "Artist " + s, Year: f.songYear[s]})
		}
		// Measured shape: the two date objects are always present and are
		// `{}` on an untagged album.
		al := map[string]any{"song": songs, "releaseDate": map[string]any{}, "originalReleaseDate": map[string]any{}}
		for k, v := range f.meta[id] {
			al[k] = v
		}
		body["album"] = al
	default:
		return "", nil
	}

	raw, err := json.Marshal(map[string]any{"subsonic-response": body})
	return string(raw), err
}

// THE BUG A REPORTER HIT ON DAY ONE. A server installed this morning has no
// plays and no favourites, so `frequent` and `recent` are both empty and
// `getStarred2` returns nothing. The pool came out empty, the run logged
// "no candidate tracks yet" and wrote not one playlist, on a library of
// thousands of tracks sitting right there.
func TestCandidatesFindsALibraryThatHasNeverBeenPlayed(t *testing.T) {
	f := newFakeServer()
	f.lists["frequent"] = nil
	f.lists["recent"] = nil
	f.lists["newest"] = []string{"a1", "a2"}
	f.albums["a1"] = []string{"s1", "s2"}
	f.albums["a2"] = []string{"s3", "s4"}

	got, err := New(f.call, "alice").Candidates(100)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("a never played library yielded %d tracks, wanted 4", len(got))
	}
}

// Every album fetched twice is a round trip spent for nothing, and `frequent`
// and `recent` overlap heavily on any real server: the same records are both
// the most played and the most recently played.
func TestCandidatesFetchesEachAlbumOnlyOnce(t *testing.T) {
	f := newFakeServer()
	f.lists["newest"] = []string{"a1", "a2"}
	f.lists["frequent"] = []string{"a1", "a2"}
	f.lists["recent"] = []string{"a1", "a2"}
	f.albums["a1"] = []string{"s1"}
	f.albums["a2"] = []string{"s2"}

	if _, err := New(f.call, "alice").Candidates(100); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	for _, id := range []string{"a1", "a2"} {
		if f.albumHi[id] != 1 {
			t.Errorf("album %s was fetched %d times, wanted 1", id, f.albumHi[id])
		}
	}
}

// The date a track was added is what New Music is supposed to rank on, and it
// arrives on every song Navidrome returns. It was being thrown away.
//
// The response here is written out by hand rather than generated, in the exact
// shape a real 0.63.1 answered with, nanoseconds and all.
func TestCandidatesKeepsTheDateATrackWasAdded(t *testing.T) {
	call := func(uri string) (string, error) {
		switch {
		case strings.HasPrefix(uri, "getAlbum?"):
			return `{"subsonic-response":{"status":"ok","album":{"song":[` +
				`{"id":"s1","title":"One","artist":"A","created":"2026-08-12T09:56:07.270870159Z"}]}}}`, nil
		case strings.HasPrefix(uri, "getAlbumList2?") && strings.Contains(uri, "type=newest"):
			return `{"subsonic-response":{"status":"ok","albumList2":{"album":[{"id":"a1"}]}}}`, nil
		}
		return `{"subsonic-response":{"status":"ok"}}`, nil
	}

	got, err := New(call, "alice").Candidates(100)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no tracks came back")
	}
	if got[0].Added.IsZero() {
		t.Fatal("the added date was dropped, so New Music has nothing to rank on")
	}
	if y := got[0].Added.Year(); y != 2026 {
		t.Fatalf("added date parsed as year %d", y)
	}
}

// The release-date list costs up to albumPages more getAlbum calls, so it is
// fetched only when the released order asked for it, and then with the window
// the wrong way round on purpose: fromYear above toYear is what makes
// Navidrome return the list newest first.
func TestCandidatesFetchesTheReleaseWindowOnlyInReleasedOrder(t *testing.T) {
	f := newFakeServer()
	f.lists["newest"] = []string{"a1"}
	f.lists["byYear"] = []string{"a2"}
	f.albums["a1"] = []string{"s1"}
	f.albums["a2"] = []string{"s2"}

	got, err := New(f.call, "alice").Candidates(100)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if n := len(f.listsOfType("byYear")); n != 0 {
		t.Fatalf("the default pool asked for the byYear list %d times", n)
	}
	if len(got) != 1 {
		t.Fatalf("default pool has %d tracks, want 1", len(got))
	}

	f = newFakeServer()
	f.lists["newest"] = []string{"a1"}
	f.lists["byYear"] = []string{"a2", "a1"}
	f.albums["a1"] = []string{"s1"}
	f.albums["a2"] = []string{"s2"}
	got, err = New(f.call, "alice").CandidatesWith(CandidateOptions{AlbumPages: 100, ByReleaseDate: true, Year: 2026})
	if err != nil {
		t.Fatalf("CandidatesWith: %v", err)
	}
	byYear := f.listsOfType("byYear")
	if len(byYear) != 1 {
		t.Fatalf("released order asked for the byYear list %d times, want 1", len(byYear))
	}
	if byYear[0].Get("fromYear") != "2026" || byYear[0].Get("toYear") != "2024" {
		t.Errorf("window was fromYear=%s toYear=%s, want 2026 down to 2024", byYear[0].Get("fromYear"), byYear[0].Get("toYear"))
	}
	if len(got) != 2 {
		t.Errorf("released pool has %d tracks, want 2", len(got))
	}
	// An album already fetched through the newest list is not fetched again.
	if f.albumHi["a1"] != 1 {
		t.Errorf("album a1 was fetched %d times, want 1", f.albumHi["a1"])
	}
}

// Precedence for the release date: the album's original release date, then
// its release date, then the album year, then the song's own year. A missing
// month or day becomes 1, and an untagged album leaves Released at zero.
func TestCandidatesPicksTheReleaseDateInTheDocumentedOrder(t *testing.T) {
	f := newFakeServer()
	f.lists["newest"] = []string{"original", "release", "year", "song", "none"}
	f.albums["original"] = []string{"s-original"}
	f.albums["release"] = []string{"s-release"}
	f.albums["year"] = []string{"s-year"}
	f.albums["song"] = []string{"s-song"}
	f.albums["none"] = []string{"s-none"}
	// The reissue shape Navidrome maps from ORIGINALDATE=1975, DATE=2015.
	f.meta["original"] = map[string]any{
		"year":                1975,
		"originalReleaseDate": map[string]any{"year": 1975, "month": 6, "day": 20},
		"releaseDate":         map[string]any{"year": 2015},
	}
	f.meta["release"] = map[string]any{"year": 2020, "releaseDate": map[string]any{"year": 2020, "month": 11}}
	f.meta["year"] = map[string]any{"year": 1999}
	f.songYear["s-song"] = 2003

	got, err := New(f.call, "alice").Candidates(100)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	byID := map[string]mixes.Track{}
	for _, tr := range got {
		byID[tr.ID] = tr
	}
	want := map[string]string{
		"s-original": "1975-06-20",
		"s-release":  "2020-11-01",
		"s-year":     "1999-01-01",
		"s-song":     "2003-01-01",
	}
	for id, day := range want {
		if got := byID[id].Released.Format("2006-01-02"); got != day {
			t.Errorf("%s released %s, want %s", id, got, day)
		}
	}
	if !byID["s-none"].Released.IsZero() {
		t.Errorf("an untagged track got a release date of %v", byID["s-none"].Released)
	}
}

// getStarred2 runs first and carries only the song's year, so a starred track
// that also sits in a fetched album would keep 1 January while the album knows
// the day. The album pass refines it, and only ever towards a more exact date.
func TestCandidatesRefinesAStarredSongFromItsAlbum(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s1", "s2"}
	f.songYear["s1"] = 2025
	f.songYear["s2"] = 2025
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"s1"}
	f.meta["a1"] = map[string]any{"year": 2025, "releaseDate": map[string]any{"year": 2025, "month": 3, "day": 14}}

	got, err := New(f.call, "alice").Candidates(100)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("pool has %d tracks, want 2 (the starred song must not be added twice)", len(got))
	}
	byID := map[string]mixes.Track{}
	for _, tr := range got {
		byID[tr.ID] = tr
	}
	if d := byID["s1"].Released.Format("2006-01-02"); d != "2025-03-14" {
		t.Errorf("starred song in a fetched album released %s, want 2025-03-14 from the album", d)
	}
	if !byID["s1"].Starred {
		t.Error("refining the date lost the starred flag")
	}
	if d := byID["s2"].Released.Format("2006-01-02"); d != "2025-01-01" {
		t.Errorf("starred song outside any fetched album released %s, want 2025-01-01 from its year", d)
	}
}
