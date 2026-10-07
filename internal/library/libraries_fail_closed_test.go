package library

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// Decision D40 A (2026-10-07). The libraries setting is one value for the
// whole server, but each account sees its own libraries. Configured "1", and
// an account that can only see library 4: up to 0.9.16 scopeLibraries dropped
// the 1, sent no musicFolderId at all, and the server answered across every
// library the account sees, which is library 4, the one the setting exists to
// keep out. "Draw mixes only from these libraries" must never widen, so an
// account that sees none of the configured ids gets an empty pool and a log
// line that says why.
func TestAnAccountThatSeesNoneOfTheConfiguredLibrariesGetsNothingFromOthers(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s-lib4"}
	f.lists["newest"] = []string{"album-lib4"}
	f.albums["album-lib4"] = []string{"t-lib4"}
	var logged []string
	Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	defer func() { Logf = nil }()

	var unscoped []string
	inner := serverWithFolders(f, "4")
	call := func(uri string) (string, error) {
		endpoint, q, _ := strings.Cut(uri, "?")
		if endpoint == "getStarred2" || endpoint == "getAlbumList2" {
			p, _ := url.ParseQuery(q)
			if len(p["musicFolderId"]) == 0 {
				unscoped = append(unscoped, endpoint)
			}
		}
		return inner(uri)
	}
	p, err := New(call, "bob").Assemble(CandidateOptions{AlbumPages: 100, MusicFolderIDs: []string{"1"}}, nil, nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(unscoped) > 0 {
		t.Fatalf("%v went out with no musicFolderId, so the server answered across every library bob sees", unscoped)
	}
	if n := len(p.Tracks()); n != 0 {
		t.Fatalf("pool holds %d tracks from libraries outside the setting, want 0", n)
	}
	if !p.Complete {
		t.Fatal("the pool must be complete, or the next call fetches again for an account that can see nothing")
	}
	said := false
	for _, l := range logged {
		if strings.Contains(l, "none of the configured ids [1]") && strings.Contains(l, "bob") && strings.Contains(l, "no mixes") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the log must name the ids, the account and that no mixes are built; got %q", logged)
	}
}

// A pool parked earlier in the day, while the account could still see a
// configured library, must not survive into a call where it sees none.
func TestAParkedPoolIsDroppedWhenNoConfiguredLibraryIsVisibleAnyMore(t *testing.T) {
	f := newFakeServer()
	call := serverWithFolders(f, "4")
	parked := &Pool{Key: "k", Format: PoolFormat, Items: []poolTrack{{ID: "t-lib1"}}, Starred: true, Seen: []string{"a-lib1"}}
	p, err := New(call, "bob").Assemble(CandidateOptions{AlbumPages: 100, MusicFolderIDs: []string{"1"}}, parked, nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if n := len(p.Tracks()); n != 0 || !p.Complete {
		t.Fatalf("pool has %d tracks, complete=%v; want 0 and complete", n, p.Complete)
	}
	if p.Key != "k" || p.Format != PoolFormat {
		t.Fatalf("key/format = %q/%d, want k/%d", p.Key, p.Format, PoolFormat)
	}
}
