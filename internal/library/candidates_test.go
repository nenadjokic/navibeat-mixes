package library

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
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
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		lists:   map[string][]string{},
		albums:  map[string][]string{},
		calls:   map[string]int{},
		albumHi: map[string]int{},
	}
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
		Created string `json:"created,omitempty"`
	}
	body := map[string]any{"status": "ok"}

	switch endpoint {
	case "getStarred2":
		songs := make([]child, 0, len(f.starred))
		for _, id := range f.starred {
			songs = append(songs, child{ID: id, Title: id, Artist: "Artist " + id})
		}
		body["starred2"] = map[string]any{"song": songs}
	case "getAlbumList2":
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
			songs = append(songs, child{ID: s, Title: s, Artist: "Artist " + s})
		}
		body["album"] = map[string]any{"song": songs}
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
