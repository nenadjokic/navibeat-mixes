package library

import "testing"

// #502323, the last rung of the watchdog's ladder. getStarred2 takes no size
// parameter, so on a server that kills a single call there is no way to ask
// for less of it: the only smaller bite is not asking at all. The phase has
// to be marked done in the same breath, because a phase left pending would
// be retried by the very next call, which is the loop this is breaking.
func TestSkipStarredDropsTheCallAndDoesNotLeaveThePhasePending(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}

	pool, err := New(f.call, "alice").Assemble(CandidateOptions{AlbumPages: 100, SkipStarred: true}, nil, nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if f.calls["getStarred2"] != 0 {
		t.Fatalf("getStarred2 was called %d times with SkipStarred set", f.calls["getStarred2"])
	}
	if !pool.Starred {
		t.Fatal("the starred phase is marked done, or the next call retries the call we just refused to make")
	}
	if !pool.Complete {
		t.Fatal("the rest of the pool still has to be assembled")
	}
	// The album tracks are still there: skipping is a loss of coverage, not
	// of the run.
	if len(pool.Tracks()) == 0 {
		t.Fatal("the album lists are still fetched when the starred call is skipped")
	}
}

// The same server without the flag: the call is made, and this is what the
// skip is measured against.
func TestTheStarredCallIsMadeWhenTheBiteIsFull(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"a1-x"}

	pool, err := New(f.call, "alice").Assemble(CandidateOptions{AlbumPages: 100}, nil, nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if f.calls["getStarred2"] != 1 {
		t.Fatalf("getStarred2 was called %d times, want 1", f.calls["getStarred2"])
	}
	if len(pool.Tracks()) != 2 {
		t.Fatalf("pool has %d tracks, want the starred one and the album one", len(pool.Tracks()))
	}
}
