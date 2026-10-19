package library

import (
	"strings"
	"testing"
)

func TestRenamesInPlaceWhenThePrefixChanges(t *testing.T) {
	s := &fakeNavidrome{lists: []Playlist{
		{ID: "a1", Name: "Old Artist Radio 1", Owner: "bob", Comment: marker("artistradio", "artistradio-1")},
	}}
	c := New(s.call, "bob")
	for i := 0; i < 2; i++ {
		id, created, err := c.EnsureMix("artistradio", "artistradio-1", "New Artist Radio 1", "New Artist Radio 1")
		if err != nil || id != "a1" || created {
			t.Fatalf("id=%q created=%v err=%v", id, created, err)
		}
	}
	if s.name("a1") != "New Artist Radio 1" || s.did("create") != 0 {
		t.Errorf("name=%q ops=%v", s.name("a1"), s.ops)
	}
	// The second call already had the wanted name and sent no rename.
	if s.did("rename") != 1 {
		t.Errorf("renamed %d times, want once", s.did("rename"))
	}
}

func TestRenamesAMixTheUserRenamedByHand(t *testing.T) {
	s := &fakeNavidrome{lists: []Playlist{
		{ID: "mine", Name: "my own title", Owner: "bob", Comment: marker("loved", "loved")},
	}}
	id, _, err := New(s.call, "bob").EnsureMix("loved", "loved", "Loved Songs", "Loved Songs")
	if err != nil || id != "mine" || s.name("mine") != "Loved Songs" {
		t.Errorf("id=%q err=%v name=%q", id, err, s.name("mine"))
	}
}

// An admin's getPlaylists holds everybody's playlists, and a marker is the
// same for every user.
func TestNeverTouchesAnotherUsersPlaylist(t *testing.T) {
	s := &fakeNavidrome{lists: []Playlist{
		{ID: "alice1", Name: "Morning", Owner: "alice", Comment: marker("timeofday", "morning")},
		{ID: "alice2", Name: "Evening", Owner: "alice"},
	}}
	for _, want := range []string{"Morning", "Evening", "New Name"} {
		id, created, err := New(s.call, "admin").EnsureMix("timeofday", "morning", want, want)
		if err != nil || strings.HasPrefix(id, "alice") || !created {
			t.Errorf("%s: id=%q created=%v err=%v", want, id, created, err)
		}
	}
	if s.name("alice1") != "Morning" || s.name("alice2") != "Evening" || s.did("rename") != 0 {
		t.Errorf("another user's playlist was touched: %v", s.ops)
	}
}

func TestEnsurePlaylistStillMatchesByNameAndOwner(t *testing.T) {
	s := &fakeNavidrome{lists: []Playlist{
		{ID: "x", Name: "N", Owner: "alice"},
		{ID: "y", Name: "N", Owner: "bob"},
	}}
	if id, err := New(s.call, "bob").EnsurePlaylist("N"); err != nil || id != "y" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestFailedRenameIsLoggedAndStillReturnsThePlaylist(t *testing.T) {
	lines := captureLog(t)
	s := &fakeNavidrome{failRename: true, lists: []Playlist{
		{ID: "a1", Name: "Old", Owner: "bob", Comment: marker("loved", "loved")},
	}}
	id, created, err := New(s.call, "bob").EnsureMix("loved", "loved", "New", "New")
	if err != nil || id != "a1" || created || s.did("create") != 0 {
		t.Fatalf("id=%q created=%v err=%v ops=%v", id, created, err, s.ops)
	}
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "could not rename") {
		t.Errorf("log = %q", *lines)
	}
}

func TestLogsWhenTheRenameTargetIsHeldByAnotherPlaylistOfTheOwner(t *testing.T) {
	lines := captureLog(t)
	s := &fakeNavidrome{lists: []Playlist{
		{ID: "mine", Name: "Old", Owner: "bob", Comment: marker("genreradio", "genreradio-1")},
		{ID: "alices", Name: "Punk Radio", Owner: "alice"},
		{ID: "other", Name: "Punk Radio", Owner: "bob", Comment: marker("genreradio", "genreradio-2")},
	}}
	if _, _, err := New(s.call, "bob").EnsureMix("genreradio", "genreradio-1", "Punk Radio", "Punk Radio"); err != nil {
		t.Fatal(err)
	}
	if s.name("mine") != "Punk Radio" {
		t.Error("the rename must still happen")
	}
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "mine") || !strings.Contains((*lines)[0], "other") || strings.Contains((*lines)[0], "alices") {
		t.Errorf("want one line naming mine and other only, got %q", *lines)
	}
}

func TestACreatedPlaylistIsFoundByItsMarkerOnTheNextCall(t *testing.T) {
	s := &fakeNavidrome{}
	c := New(s.call, "bob")
	id, created, err := c.EnsureMix("loved", "loved", "Loved", "Loved")
	if err != nil || !created {
		t.Fatalf("id=%q created=%v err=%v", id, created, err)
	}
	if err := c.SetComment(id, marker("loved", "loved")); err != nil {
		t.Fatal(err)
	}
	// "Renamed" matches no name, so only the marker can lead back to it.
	again, created, _ := c.EnsureMix("loved", "loved", "Renamed", "Renamed")
	if again != id || created || s.did("create") != 1 || s.listCalls != 1 {
		t.Errorf("again=%q created=%v ops=%v listCalls=%d", again, created, s.ops, s.listCalls)
	}
}
