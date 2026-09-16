package library

import (
	"net/url"
	"strings"
	"testing"
)

// #502335 (James): the pool must ask the server for the configured libraries
// only, on the starred call and on every album list, and must ask for nothing
// in particular when no library is configured.
func TestConfiguredLibrariesAreSentOnEveryPoolCall(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}

	var starredURIs []string
	call := func(uri string) (string, error) {
		if strings.HasPrefix(uri, "getStarred2") {
			starredURIs = append(starredURIs, uri)
		}
		return f.call(uri)
	}
	opts := CandidateOptions{AlbumPages: 100, MusicFolderIDs: []string{"3", "7"}}
	if _, err := New(call, "alice").Assemble(opts, nil, nil); err != nil {
		t.Fatalf("assemble: %v", err)
	}

	if len(starredURIs) != 1 {
		t.Fatalf("getStarred2 called %d times, want 1", len(starredURIs))
	}
	_, q, _ := strings.Cut(starredURIs[0], "?")
	got, _ := url.ParseQuery(q)
	if ids := got["musicFolderId"]; len(ids) != 2 || ids[0] != "3" || ids[1] != "7" {
		t.Fatalf("getStarred2 musicFolderId=%v, want [3 7]", ids)
	}
	if len(f.listParams) == 0 {
		t.Fatal("no getAlbumList2 call recorded")
	}
	for _, p := range f.listParams {
		if ids := p["musicFolderId"]; len(ids) != 2 || ids[0] != "3" || ids[1] != "7" {
			t.Fatalf("getAlbumList2 %s musicFolderId=%v, want [3 7]", p.Get("type"), ids)
		}
	}
}

// The default: nothing configured, nothing sent, the query is what it always
// was. This is the half that keeps every existing install unchanged.
func TestNoConfiguredLibrariesSendsNoFolderParameter(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}

	var starredURIs []string
	call := func(uri string) (string, error) {
		if strings.HasPrefix(uri, "getStarred2") {
			starredURIs = append(starredURIs, uri)
		}
		return f.call(uri)
	}
	if _, err := New(call, "alice").Assemble(CandidateOptions{AlbumPages: 100}, nil, nil); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if strings.Contains(starredURIs[0], "musicFolderId") {
		t.Fatalf("getStarred2 carried a folder with none configured: %s", starredURIs[0])
	}
	for _, p := range f.listParams {
		if _, has := p["musicFolderId"]; has {
			t.Fatalf("getAlbumList2 %s carried a folder with none configured", p.Get("type"))
		}
	}
}
