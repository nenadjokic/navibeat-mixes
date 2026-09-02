package library

import (
	"strconv"
	"testing"
)

// stepPacer says the budget is spent after a fixed number of pages, so a test
// can stop the pool at any page it likes without a clock.
type stepPacer struct{ left int }

func (s *stepPacer) Step()       { s.left-- }
func (s *stepPacer) Spent() bool { return s.left <= 0 }

// THE 2026-09-02 FAILURE. On a loaded NAS the pool alone outran the host's
// 30 seconds, and the old code checked the budget only after the pool, so it
// was killed with nothing written, every night. Stopping after any page and
// continuing from a parked pool has to reach the same pool as one uninterrupted
// fetch, with every album fetched exactly once across the calls.
func TestAssembleContinuesFromAParkedPoolWithoutRefetching(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1", "a2", "a3"}
	f.lists["frequent"] = []string{"a2", "a4"}
	f.lists["recent"] = []string{"a4", "a5"}
	for _, a := range []string{"a1", "a2", "a3", "a4", "a5"} {
		f.albums[a] = []string{a + "-x", a + "-y"}
	}
	opts := CandidateOptions{AlbumPages: 100}

	// One uninterrupted fetch is the reference.
	whole, err := New(f.call, "alice").CandidatesWith(opts)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	if len(whole) != 11 {
		t.Fatalf("reference pool has %d tracks, want 11", len(whole))
	}

	// Now the same server, two pages per call, parked and reloaded between
	// calls the way the plugin does it.
	f = newFakeServer()
	f.starred = []string{"s0"}
	f.lists["newest"] = []string{"a1", "a2", "a3"}
	f.lists["frequent"] = []string{"a2", "a4"}
	f.lists["recent"] = []string{"a4", "a5"}
	for _, a := range []string{"a1", "a2", "a3", "a4", "a5"} {
		f.albums[a] = []string{a + "-x", a + "-y"}
	}
	var parked []byte
	calls := 0
	for {
		calls++
		if calls > 20 {
			t.Fatal("the pool never completed")
		}
		pool := DecodePool(parked, "run")
		pool, err := New(f.call, "alice").Assemble(opts, pool, &stepPacer{left: 2})
		if err != nil {
			t.Fatalf("call %d: %v", calls, err)
		}
		pool.Key = "run"
		if pool.Complete {
			got := pool.Tracks()
			if len(got) != len(whole) {
				t.Fatalf("resumed pool has %d tracks, want %d", len(got), len(whole))
			}
			for i := range whole {
				if got[i].ID != whole[i].ID {
					t.Fatalf("track %d is %s, want %s (order must match one fetch)", i, got[i].ID, whole[i].ID)
				}
			}
			break
		}
		parked = pool.Encode()
	}
	// 1 getStarred2 + 3 getAlbumList2 + 5 getAlbum = 9 pages, two per call,
	// and the last call finds nothing left and only sets Complete.
	if calls < 5 {
		t.Fatalf("finished in %d calls, the pacer should have stopped it more often", calls)
	}
	for _, id := range []string{"a1", "a2", "a3", "a4", "a5"} {
		if f.albumHi[id] != 1 {
			t.Errorf("album %s was fetched %d times across the calls, want 1", id, f.albumHi[id])
		}
	}
	if f.calls["getAlbumList2"] != 3 {
		t.Errorf("getAlbumList2 was called %d times, want 3", f.calls["getAlbumList2"])
	}
	if f.calls["getStarred2"] != 1 {
		t.Errorf("getStarred2 was called %d times, want 1", f.calls["getStarred2"])
	}
}

// A pool from another run (another day, other options) or another build of
// the plugin is refused, so a continuation can only ever pick up its own work.
func TestDecodePoolRefusesAnotherRunOrFormat(t *testing.T) {
	p := &Pool{Key: "2026-09-02|100|added", Starred: true}
	data := p.Encode()
	if DecodePool(data, "2026-09-02|100|added") == nil {
		t.Fatal("a pool from this run must decode")
	}
	if DecodePool(data, "2026-09-03|100|added") != nil {
		t.Fatal("yesterday's pool must not feed today's mixes")
	}
	if DecodePool([]byte(`{"k":"2026-09-02|100|added","f":0,"s":true}`), "2026-09-02|100|added") != nil {
		t.Fatal("a pool in another format must be refused")
	}
	if DecodePool([]byte("{not json"), "x") != nil || DecodePool(nil, "x") != nil {
		t.Fatal("garbage must decode to nothing")
	}
}

// The stored form keeps every field a builder reads and drops the rest, and
// it has to be small: the kvstore is capped at 1 MB and the listening
// histories share it. Measured here so a field added later shows its cost.
func TestPoolRoundTripKeepsWhatTheBuildersReadAndStaysSmall(t *testing.T) {
	f := newFakeServer()
	f.starred = []string{"s1"}
	f.songYear["s1"] = 2001
	f.lists["newest"] = []string{"a1"}
	f.albums["a1"] = []string{"s1", "s2"}
	f.meta["a1"] = map[string]any{"year": 2001, "releaseDate": map[string]any{"year": 2001, "month": 5, "day": 6}}

	pool, err := New(f.call, "alice").Assemble(CandidateOptions{AlbumPages: 10}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool.Key = "run"
	back := DecodePool(pool.Encode(), "run")
	if back == nil {
		t.Fatal("round trip lost the pool")
	}
	got := back.Tracks()
	if len(got) != 2 {
		t.Fatalf("pool has %d tracks, want 2", len(got))
	}
	s1 := got[0]
	if !s1.Starred || s1.Artist != "Artist s1" || s1.Year != 2001 {
		t.Fatalf("s1 came back as %+v", s1)
	}
	if d := s1.Released.Format("2006-01-02"); d != "2001-05-06" {
		t.Fatalf("s1 released %s, want the album's day 2001-05-06", d)
	}

	// Size: a pool of 3000 tracks with realistic ids and names. Navidrome ids
	// are 32 random hex digits, which no compressor shrinks much, so the ids
	// here are pseudo-random rather than one repeated string.
	big := &Pool{Key: "run", Starred: true, Complete: true}
	seed := uint64(88172645463325252)
	hexID := func() string {
		var b []byte
		for len(b) < 32 {
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			b = strconv.AppendUint(b, seed, 16)
		}
		return string(b[:32])
	}
	for i := 0; i < 3000; i++ {
		big.Items = append(big.Items, poolTrack{
			ID: hexID(), Artist: "The Longer Band Name " + strconv.Itoa(i%40),
			Genre: "Alternative Rock", Genres: []string{"Alternative Rock", "Indie"},
			Year: 1990 + i%36, PlayCount: i % 50, LastPlayed: 1756800000, Added: 1756700000,
			Released: 946684800, Precision: 3, Starred: i%7 == 0,
		})
		if i%10 == 0 {
			big.Seen = append(big.Seen, hexID())
		}
	}
	size := len(big.Encode())
	t.Logf("3000 tracks park as %d bytes (%d per track)", size, size/3000)
	if size > 256*1024 {
		t.Fatalf("3000 tracks take %d bytes, too much of the 1 MB kvstore cap", size)
	}
	if DecodePool(big.Encode(), "run") == nil {
		t.Fatal("the big pool did not survive its own round trip")
	}
}
