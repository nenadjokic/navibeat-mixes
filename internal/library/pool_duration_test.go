package library

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// On Repeat dedups the server's play history with the collector's rule,
// max(60s, track duration), so the duration Subsonic sends must survive the
// pool and reach the builders.
func TestPoolCarriesTrackDurationToTheBuilders(t *testing.T) {
	call := func(uri string) (string, error) {
		endpoint, _, _ := strings.Cut(uri, "?")
		body := map[string]any{"status": "ok"}
		switch endpoint {
		case "getAlbumList2":
			body["albumList2"] = map[string]any{"album": []map[string]string{{"id": "a1"}}}
		case "getAlbum":
			body["album"] = map[string]any{"song": []map[string]any{
				{"id": "s1", "title": "s1", "artist": "X", "duration": 245},
			}}
		case "getStarred2":
			body["starred2"] = map[string]any{}
		}
		raw, err := json.Marshal(map[string]any{"subsonic-response": body})
		return string(raw), err
	}
	pool, err := New(call, "alice").Assemble(CandidateOptions{AlbumPages: 10}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool.Key = "run"
	back := DecodePool(pool.Encode(), "run")
	if back == nil {
		t.Fatal("round trip lost the pool")
	}
	got := back.Tracks()
	if len(got) != 1 || got[0].Duration != 245*time.Second {
		t.Fatalf("tracks = %+v, want s1 with a 245s duration", got)
	}
}
