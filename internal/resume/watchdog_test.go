package resume

import "testing"

// watchStore is the kvstore slice the watchdog needs, with a TTL that is
// recorded rather than enforced: no test here needs a clock, and what matters
// is that the mark is written with an expiry at all.
type watchStore struct {
	kv  map[string][]byte
	ttl map[string]int64
}

func newWatchStore() *watchStore {
	return &watchStore{kv: map[string][]byte{}, ttl: map[string]int64{}}
}

func (s *watchStore) Get(key string) ([]byte, bool, error) {
	v, ok := s.kv[key]
	return v, ok, nil
}
func (s *watchStore) Set(key string, value []byte) error { s.kv[key] = value; return nil }
func (s *watchStore) Delete(key string) error            { delete(s.kv, key); return nil }
func (s *watchStore) SetWithTTL(key string, value []byte, ttlSeconds int64) error {
	s.kv[key] = value
	s.ttl[key] = ttlSeconds
	return nil
}

// THE 2026-09-13 REPORT (#502323). A Raspberry Pi 3B, Navidrome 0.64.0, the
// callback killed at 30.79s with budgetSeconds set to 3. The budget cannot
// see that: it is read between units of work and the plugin died inside one.
// A killed call also saves no ledger, so the stall counter never moves and
// every continuation repeats the same bite.
//
// The mark is written before the work, so the kill leaves it behind, and the
// next call reads it as evidence and takes less.
func TestAKilledCallLeavesItsMarkAndTheNextCallTakesASmallerBite(t *testing.T) {
	s := newWatchStore()

	first := NextWatch(s)
	if first.Kills != 0 {
		t.Fatalf("a clean store means no kills, got %d", first.Kills)
	}
	full := first.Bite(100, 0)
	if full.AlbumPages != 100 || full.SkipStarred || full.GiveUp {
		t.Fatalf("the first call takes the full bite, got %+v", full)
	}
	if err := first.Arm(s, "pool", "alice"); err != nil {
		t.Fatalf("arm: %v", err)
	}
	if s.ttl[WatchKey] != WatchTTLSec {
		t.Fatalf("the mark is written with an expiry, got ttl %d", s.ttl[WatchKey])
	}
	// The host kills the module here. Nothing else runs: no ledger save, no
	// clear, no deferred anything. The mark is simply still there.

	second := NextWatch(s)
	if second.Kills != 1 {
		t.Fatalf("the mark left behind is one kill, got %d", second.Kills)
	}
	if second.Phase != "pool" || second.User != "alice" {
		t.Fatalf("the mark says where the work died, got phase %q user %q", second.Phase, second.User)
	}
	smaller := second.Bite(100, 0)
	if smaller.AlbumPages >= full.AlbumPages {
		t.Fatalf("the second call must ask for less, got %d after %d", smaller.AlbumPages, full.AlbumPages)
	}
	if smaller.GiveUp {
		t.Fatal("one kill is not a reason to give up")
	}
}

// A call that returns is not a call that was killed, even when it returned
// with the work unfinished. Parking a half built pool and asking for a
// continuation is the healthy path, and the next call is owed a full bite.
func TestACallThatReturnsClearsTheMarkSoTheNextBiteIsFull(t *testing.T) {
	s := newWatchStore()
	w := NextWatch(s)
	if err := w.Arm(s, "pool", "alice"); err != nil {
		t.Fatalf("arm: %v", err)
	}
	ClearWatch(s)

	next := NextWatch(s)
	if next.Kills != 0 {
		t.Fatalf("a cleared mark means no kill, got %d", next.Kills)
	}
	if b := next.Bite(100, 0); b.AlbumPages != 100 || b.SkipStarred {
		t.Fatalf("the next bite is the full one, got %+v", b)
	}
}

// The ladder is finite on purpose. getStarred2 takes no size parameter in the
// Subsonic API, so once the pages are at their smallest and that call is out
// of the run there is nothing left to shrink, and a server that still kills
// the call cannot be served today. Repeating the chain every five seconds
// would be the plugin hammering a machine it has already proved too slow.
func TestTheLadderShrinksThenGivesUpRatherThanRepeating(t *testing.T) {
	s := newWatchStore()
	var pages []int
	var skipped, gaveUp int
	for call := 0; call < 6; call++ {
		w := NextWatch(s)
		b := w.Bite(100, 0)
		pages = append(pages, b.AlbumPages)
		if b.SkipStarred {
			skipped++
		}
		if b.GiveUp {
			gaveUp++
			break
		}
		if err := w.Arm(s, "pool", "alice"); err != nil {
			t.Fatalf("arm: %v", err)
		}
		// killed again
	}
	if gaveUp != 1 {
		t.Fatalf("the chain has to end in giving up exactly once, got %d (pages %v)", gaveUp, pages)
	}
	for i := 1; i < len(pages); i++ {
		if pages[i] > pages[i-1] {
			t.Fatalf("the bite never grows while calls are being killed, got %v", pages)
		}
	}
	if pages[len(pages)-1] >= pages[0] {
		t.Fatalf("the last bite has to be smaller than the first, got %v", pages)
	}
	if skipped == 0 {
		t.Fatalf("the unpaginated call is dropped before giving up, got pages %v", pages)
	}
}

// A mark nobody can read is still a mark somebody left. Treating it as a
// clean start would hide the exact failure the watchdog exists to catch.
func TestAnUnreadableMarkStillCountsAsAKill(t *testing.T) {
	s := newWatchStore()
	if err := s.SetWithTTL(WatchKey, []byte("{not json"), WatchTTLSec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := NextWatch(s).Kills; got != 1 {
		t.Fatalf("an unreadable mark is one kill, got %d", got)
	}
}

// Bite never returns a page size the Subsonic call cannot use, whatever the
// full size is. size=0 would ask Navidrome for its own default, which on this
// path is the opposite of a smaller bite.
func TestEveryRungAsksForAtLeastOneAlbum(t *testing.T) {
	for kills := 0; kills < len(biteLadder)+2; kills++ {
		w := &Watch{Kills: kills}
		if got := w.Bite(3, 0).AlbumPages; got < 1 {
			t.Fatalf("kills %d gave page size %d", kills, got)
		}
	}
}

// THE OSCILLATION, and it is the reason the pace exists at all. The mark is
// cleared by every call that returns, and the call that returns is exactly
// the one that proved the smaller bite fits. Read the kills alone and the
// next call goes straight back to the full bite and is killed again, so a
// slow server spends two dead calls for every call that gets anything done.
func TestABiteThatSurvivedIsNotThrownAwayByTheNextCall(t *testing.T) {
	s := newWatchStore()
	const day = "2026-09-13"

	// Two kills, then a call at the third rung that returns.
	var bite Bite
	for call := 0; call < 3; call++ {
		w := NextWatch(s)
		bite = w.Bite(100, LoadPace(s, day))
		if err := w.Arm(s, "pool", "alice"); err != nil {
			t.Fatalf("arm: %v", err)
		}
	}
	if bite.Rung != 2 {
		t.Fatalf("two kills put the third call on rung %d, want 2", bite.Rung)
	}
	// This one returns: mark cleared, rung remembered.
	ClearWatch(s)
	if err := SavePace(s, day, bite.Rung); err != nil {
		t.Fatalf("save pace: %v", err)
	}

	next := NextWatch(s)
	if next.Kills != 0 {
		t.Fatalf("the call returned, so there is no kill, got %d", next.Kills)
	}
	after := next.Bite(100, LoadPace(s, day))
	if after.Rung != bite.Rung || after.AlbumPages != bite.AlbumPages {
		t.Fatalf("the next call went back to rung %d (%d albums) after rung %d (%d albums) survived",
			after.Rung, after.AlbumPages, bite.Rung, bite.AlbumPages)
	}
	if after.GiveUp {
		t.Fatal("a bite that works is not a reason to give up")
	}
}

// A kill from the remembered floor steps DOWN from it, not back to the top.
func TestAKillBelowTheRememberedFloorStepsDownFromIt(t *testing.T) {
	s := newWatchStore()
	const day = "2026-09-13"
	if err := SavePace(s, day, 2); err != nil {
		t.Fatalf("save pace: %v", err)
	}
	w := NextWatch(s)
	if err := w.Arm(s, "pool", "alice"); err != nil {
		t.Fatalf("arm: %v", err)
	}
	killed := NextWatch(s).Bite(100, LoadPace(s, day))
	if killed.Rung != 3 {
		t.Fatalf("floor 2 plus one kill is rung %d, want 3", killed.Rung)
	}
	if !killed.SkipStarred {
		t.Fatal("rung 3 is the one that drops the unpaginated call")
	}
}

// Yesterday's pace says nothing about today's server, the same rule the
// ledger has always used for its day.
func TestThePaceIsForgottenOnANewDay(t *testing.T) {
	s := newWatchStore()
	if err := SavePace(s, "2026-09-12", 3); err != nil {
		t.Fatalf("save pace: %v", err)
	}
	if got := LoadPace(s, "2026-09-13"); got != 0 {
		t.Fatalf("a pace from another day is not today's, got rung %d", got)
	}
}

// A rung of zero is not a pace, it is the absence of one, and writing it
// would turn "nothing learned" into a stored fact.
func TestAFullBiteIsNotRememberedAsAPace(t *testing.T) {
	s := newWatchStore()
	if err := SavePace(s, "2026-09-13", 0); err != nil {
		t.Fatalf("save pace: %v", err)
	}
	if _, ok, _ := s.Get(PaceKey); ok {
		t.Fatal("rung 0 wrote a pace")
	}
}
