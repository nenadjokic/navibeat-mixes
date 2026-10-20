package library

import "testing"

// A new playlist named from a template gets its description (and so its
// marker) before its tracks. When that write fails twice the slot fails and
// the playlist is left behind empty, unmarked and without the attribution
// line. The retry must adopt it, not create a second playlist next to it.
func TestRetryAdoptsTheEmptyPlaylistLeftByAFailedDescription(t *testing.T) {
	s := &fakeNavidrome{failComment: 2}
	c := New(s.call, "bob")
	id, created, err := c.EnsureMix("artistradio", "artistradio-1", "Radio: Billy Talent", "Artist Radio 1")
	if err != nil || !created {
		t.Fatalf("first pass: id=%q created=%v err=%v", id, created, err)
	}
	if err := c.WriteMix(id, created, []string{"t1"}, marker("artistradio", "artistradio-1")); err == nil {
		t.Fatal("expected the description write to fail")
	}
	// The next pass is a new client, like the next scheduler call.
	id2, created2, err := New(s.call, "bob").EnsureMix("artistradio", "artistradio-1", "Radio: Billy Talent", "Artist Radio 1")
	if err != nil || created2 || id2 != id {
		t.Errorf("retry: id=%q created=%v err=%v, want %q adopted", id2, created2, err, id)
	}
}

// Only an EMPTY playlist with no description is adopted that way. A hand made
// playlist with the same name and tracks or text of its own stays untouched,
// and so does another user's empty playlist of that name.
func TestRetryDoesNotAdoptAPlaylistThatIsNotEmptyOrNotOurs(t *testing.T) {
	cases := map[string]Playlist{
		"has tracks":        {ID: "x", Name: "Radio: Billy Talent", Owner: "bob", SongCount: 3},
		"has a description": {ID: "x", Name: "Radio: Billy Talent", Owner: "bob", Comment: "mine"},
		"another user":      {ID: "x", Name: "Radio: Billy Talent", Owner: "alice"},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			s := &fakeNavidrome{lists: []Playlist{p}}
			id, created, err := New(s.call, "bob").EnsureMix("artistradio", "artistradio-1", "Radio: Billy Talent", "Artist Radio 1")
			if err != nil || id == "x" || !created {
				t.Errorf("id=%q created=%v err=%v", id, created, err)
			}
		})
	}
}
