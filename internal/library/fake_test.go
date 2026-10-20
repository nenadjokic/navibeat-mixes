package library

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/nenadjokic/navibeat-mixes/internal/protocol"
)

// fakeNavidrome answers the playlist calls EnsureMix and WriteMix make. It
// applies renames and descriptions to its list like the real server and
// records the writes in order: "create", "rename", "comment", "tracks".
type fakeNavidrome struct {
	lists       []Playlist
	ops         []string
	failRename  bool
	failComment int // how many description writes to fail before succeeding
	listCalls   int
}

func (s *fakeNavidrome) call(uri string) (string, error) {
	endpoint, query, _ := strings.Cut(uri, "?")
	p, _ := url.ParseQuery(query)
	const ok = `{"subsonic-response":{"status":"ok"}}`
	switch {
	case endpoint == "getPlaylists":
		s.listCalls++
		var items []string
		for _, l := range s.lists {
			items = append(items, fmt.Sprintf(`{"id":%q,"name":%q,"comment":%q,"owner":%q,"created":%q}`,
				l.ID, l.Name, l.Comment, l.Owner, l.Created))
		}
		return `{"subsonic-response":{"status":"ok","playlists":{"playlist":[` + strings.Join(items, ",") + `]}}}`, nil
	case endpoint == "createPlaylist" && p.Get("playlistId") != "":
		s.ops = append(s.ops, "tracks")
	case endpoint == "createPlaylist":
		s.ops = append(s.ops, "create")
		id := fmt.Sprintf("new%d", len(s.lists)+1)
		s.lists = append(s.lists, Playlist{ID: id, Name: p.Get("name"), Owner: p.Get("u")})
		return `{"subsonic-response":{"status":"ok","playlist":{"id":"` + id + `"}}}`, nil
	case endpoint == "updatePlaylist" && p.Has("name"):
		s.ops = append(s.ops, "rename")
		if s.failRename {
			return `{"subsonic-response":{"status":"failed","error":{"code":0,"message":"nope"}}}`, nil
		}
		s.update(p.Get("playlistId"), func(l *Playlist) { l.Name = p.Get("name") })
	case endpoint == "updatePlaylist" && p.Has("comment"):
		s.ops = append(s.ops, "comment")
		if s.failComment > 0 {
			s.failComment--
			return "", errors.New("boom")
		}
		s.update(p.Get("playlistId"), func(l *Playlist) { l.Comment = p.Get("comment") })
	}
	return ok, nil
}

func (s *fakeNavidrome) update(id string, f func(*Playlist)) {
	for i := range s.lists {
		if s.lists[i].ID == id {
			f(&s.lists[i])
		}
	}
}

func (s *fakeNavidrome) name(id string) string {
	for _, l := range s.lists {
		if l.ID == id {
			return l.Name
		}
	}
	return ""
}

func (s *fakeNavidrome) did(op string) int {
	n := 0
	for _, o := range s.ops {
		if o == op {
			n++
		}
	}
	return n
}

// marker is the description a mix written by the plugin carries.
func marker(kind, slot string) string {
	return markerOn(kind, slot, "2026-01-01")
}

// markerOn is the same with the generation date the plugin wrote.
func markerOn(kind, slot, date string) string {
	return protocol.Format("human", protocol.Meta{Kind: kind, Slot: slot, Date: date, Mode: "affinity", Count: 30})
}

// captureLog collects what the package logs until the test ends.
func captureLog(t *testing.T) *[]string {
	var lines []string
	Logf = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	t.Cleanup(func() { Logf = nil })
	return &lines
}
