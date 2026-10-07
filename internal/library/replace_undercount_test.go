package library

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// navibeat-mixes#8. getPlaylist reports songCount as the number of rows the
// CALLER can see that are not missing (Navidrome persistence/
// playlist_repository.go GetWithTracks filters missing=false and applies
// applyLibraryFilter; model/playlist.go refreshStats sets SongCount =
// len(Tracks)). updatePlaylist removes by POSITION (core/playlists/
// playlists.go Update: position = index+1, then renumber) and appends after
// max(position). So a rewrite that cleared 0..songCount-1 kept every row the
// count did not include, at the head of the playlist.
//
// playlistServer models those server rules, plus createPlaylist with
// playlistId, which replaces every row (core/playlists/playlists.go Create,
// persistence updatePlaylist deletes all playlist_tracks for the id).
//
// This test was first run against the 0.9.15 code path (TrackCount, then
// ReplaceTracks clearing 0..count-1) and failed with
// [gone2 old3 n1 .. n10] instead of [n1 .. n10].
type playlistServer struct {
	rows   []string        // media file ids, position i+1
	hidden map[string]bool // missing, or in a library the caller cannot see
	calls  []string
	last   url.Values
}

func (s *playlistServer) call(uri string) (string, error) {
	endpoint, query, _ := strings.Cut(uri, "?")
	p, _ := url.ParseQuery(query)
	s.calls = append(s.calls, endpoint)
	s.last = p
	body := map[string]any{"status": "ok"}
	switch endpoint {
	case "getPlaylist":
		visible := 0
		for _, id := range s.rows {
			if !s.hidden[id] {
				visible++
			}
		}
		body["playlist"] = map[string]any{"id": "pl1", "songCount": visible}
	case "updatePlaylist":
		if rm := p["songIndexToRemove"]; len(rm) > 0 {
			drop := map[int]bool{}
			for _, v := range rm {
				var i int
				_ = json.Unmarshal([]byte(v), &i)
				drop[i] = true
			}
			kept := s.rows[:0:0]
			for i, id := range s.rows {
				if !drop[i] {
					kept = append(kept, id)
				}
			}
			s.rows = kept
		}
		s.rows = append(s.rows, p["songIdToAdd"]...)
	case "createPlaylist":
		if p.Get("playlistId") == "" {
			body["status"] = "failed"
			break
		}
		s.rows = append([]string(nil), p["songId"]...)
		body["playlist"] = map[string]any{"id": p.Get("playlistId")}
	}
	raw, err := json.Marshal(map[string]any{"subsonic-response": body})
	return string(raw), err
}

func TestARewriteLeavesExactlyTheNewTracksEvenWhenSomeOldRowsAreNotCounted(t *testing.T) {
	s := &playlistServer{
		// Yesterday's mix: two of its tracks are now missing, or sit in a
		// library this account no longer sees.
		rows:   []string{"old1", "gone1", "old2", "gone2", "old3"},
		hidden: map[string]bool{"gone1": true, "gone2": true},
	}
	c := New(s.call, "alice")
	want := []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8", "n9", "n10"}

	if err := c.ReplaceTracks("pl1", want); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if strings.Join(s.rows, ",") != strings.Join(want, ",") {
		t.Fatalf("playlist after rewrite = %v, want exactly %v: rows the count did not include survived", s.rows, want)
	}
	// One call, and it must be the replace-in-place form: with a name and no
	// playlistId, createPlaylist would make a second playlist instead.
	if strings.Join(s.calls, ",") != "createPlaylist" {
		t.Fatalf("calls = %v, want a single createPlaylist", s.calls)
	}
	if s.last.Get("playlistId") != "pl1" || s.last.Has("name") {
		t.Fatalf("createPlaylist params = %v, want playlistId=pl1 and no name", s.last)
	}
	if s.last.Get("u") != "alice" {
		t.Fatalf("createPlaylist acted as %q, want the playlist owner alice", s.last.Get("u"))
	}
}

func TestAnEmptyRewriteIsRefusedRatherThanSentAsANoOp(t *testing.T) {
	// Navidrome's Put only rewrites tracks when the new list is non-empty, so
	// an empty createPlaylist would quietly keep yesterday's mix.
	s := &playlistServer{rows: []string{"old1"}}
	c := New(s.call, "alice")
	if err := c.ReplaceTracks("pl1", nil); err == nil {
		t.Fatal("empty replace returned no error")
	}
	if len(s.calls) != 0 {
		t.Fatalf("empty replace reached the server: %v", s.calls)
	}
}
