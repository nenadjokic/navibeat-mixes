package library

import (
	"strings"
	"testing"
)

func tenIDs() []string { return strings.Split("a,b,c,d,e,f,g,h,i,j", ",") }

func TestWriteMixGivesANewPlaylistItsDescriptionBeforeItsTracks(t *testing.T) {
	s := &fakeNavidrome{}
	if err := New(s.call, "bob").WriteMix("p", true, tenIDs(), "c"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.ops, ","); got != "comment,tracks" {
		t.Errorf("order = %s", got)
	}
}

func TestWriteMixRetriesTheDescriptionOnceForANewPlaylist(t *testing.T) {
	s := &fakeNavidrome{failComment: 1}
	if err := New(s.call, "bob").WriteMix("p", true, tenIDs(), "c"); err != nil {
		t.Fatalf("one transient failure should be retried: %v", err)
	}
	s = &fakeNavidrome{failComment: 2}
	if err := New(s.call, "bob").WriteMix("p", true, tenIDs(), "c"); err == nil {
		t.Fatal("expected an error")
	}
	if got := strings.Join(s.ops, ","); got != "comment,comment" {
		t.Errorf("ops = %s, want two attempts and no tracks", got)
	}
}

func TestWriteMixWritesTracksFirstForAnExistingPlaylist(t *testing.T) {
	s := &fakeNavidrome{failComment: 1}
	lines := captureLog(t)
	if err := New(s.call, "bob").WriteMix("p", false, tenIDs(), "c"); err != nil {
		t.Fatalf("a failed description must not fail an existing playlist: %v", err)
	}
	if got := strings.Join(s.ops, ","); got != "tracks,comment" || len(*lines) != 1 {
		t.Errorf("order = %s, log = %q", got, *lines)
	}
}
