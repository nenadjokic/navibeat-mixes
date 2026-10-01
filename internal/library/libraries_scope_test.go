package library

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// #502717: Navidrome answers "Library N not found or not accessible" for a
// musicFolderId outside the account's libraries (server/subsonic/helpers.go,
// v0.64.2), and that fails getStarred2 and every getAlbumList2. So the pool
// asks the server which libraries the account can see and sends only the
// configured ids that are in that set, naming each dropped one in the log.

// serverWithFolders wraps the fake so getMusicFolders answers with these ids,
// the way a 0.64.2 Navidrome does (id is a JSON number there).
func serverWithFolders(f *fakeServer, ids ...string) func(string) (string, error) {
	return func(uri string) (string, error) {
		if strings.HasPrefix(uri, "getMusicFolders") {
			f.calls["getMusicFolders"]++
			parts := make([]string, 0, len(ids))
			for _, id := range ids {
				parts = append(parts, `{"id":`+id+`,"name":"lib `+id+`"}`)
			}
			return `{"subsonic-response":{"status":"ok","musicFolders":{"musicFolder":[` + strings.Join(parts, ",") + `]}}}`, nil
		}
		return f.call(uri)
	}
}

func starredFolderIDs(t *testing.T, uris []string) []string {
	t.Helper()
	if len(uris) != 1 {
		t.Fatalf("getStarred2 called %d times, want 1", len(uris))
	}
	_, q, _ := strings.Cut(uris[0], "?")
	got, _ := url.ParseQuery(q)
	return got["musicFolderId"]
}

func TestAnIdTheAccountCannotSeeIsDroppedNotSent(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}
	var logged []string
	Logf = func(format string, args ...any) { logged = append(logged, format) }
	defer func() { Logf = nil }()

	var starredURIs []string
	inner := serverWithFolders(f, "3", "7")
	call := func(uri string) (string, error) {
		if strings.HasPrefix(uri, "getStarred2") {
			starredURIs = append(starredURIs, uri)
		}
		return inner(uri)
	}
	opts := CandidateOptions{AlbumPages: 100, MusicFolderIDs: []string{"3", "9"}}
	if _, err := New(call, "alice").Assemble(opts, nil, nil); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if ids := starredFolderIDs(t, starredURIs); len(ids) != 1 || ids[0] != "3" {
		t.Fatalf("getStarred2 musicFolderId=%v, want [3]: the unknown 9 must be dropped", ids)
	}
	for _, p := range f.listParams {
		if ids := p["musicFolderId"]; len(ids) != 1 || ids[0] != "3" {
			t.Fatalf("getAlbumList2 musicFolderId=%v, want [3]", ids)
		}
	}
	if len(logged) == 0 {
		t.Fatal("dropping a configured library id must be said in the log")
	}
}

func TestEveryIdUnknownSendsNothingLikeAnEmptySetting(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}

	var starredURIs []string
	inner := serverWithFolders(f, "3")
	call := func(uri string) (string, error) {
		if strings.HasPrefix(uri, "getStarred2") {
			starredURIs = append(starredURIs, uri)
		}
		return inner(uri)
	}
	opts := CandidateOptions{AlbumPages: 100, MusicFolderIDs: []string{"8", "9"}}
	if _, err := New(call, "alice").Assemble(opts, nil, nil); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if ids := starredFolderIDs(t, starredURIs); len(ids) != 0 {
		t.Fatalf("getStarred2 musicFolderId=%v, want none: no configured id exists", ids)
	}
}

func TestAFailedFolderListKeepsTheConfiguredIds(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}

	var starredURIs []string
	call := func(uri string) (string, error) {
		if strings.HasPrefix(uri, "getMusicFolders") {
			return "", errors.New("host killed the call")
		}
		if strings.HasPrefix(uri, "getStarred2") {
			starredURIs = append(starredURIs, uri)
		}
		return f.call(uri)
	}
	opts := CandidateOptions{AlbumPages: 100, MusicFolderIDs: []string{"3", "7"}}
	if _, err := New(call, "alice").Assemble(opts, nil, nil); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if ids := starredFolderIDs(t, starredURIs); len(ids) != 2 || ids[0] != "3" || ids[1] != "7" {
		t.Fatalf("getStarred2 musicFolderId=%v, want [3 7]: an unanswered folder list must not narrow the setting", ids)
	}
}

func TestNoConfiguredLibrariesNeverAsksForFolders(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}
	call := serverWithFolders(f, "3")
	if _, err := New(call, "alice").Assemble(CandidateOptions{AlbumPages: 100}, nil, nil); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if f.calls["getMusicFolders"] != 0 {
		t.Fatalf("getMusicFolders called %d times with no libraries configured, want 0", f.calls["getMusicFolders"])
	}
}
