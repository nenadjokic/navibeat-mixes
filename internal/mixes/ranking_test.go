package mixes

import (
	"testing"
	"time"
)

// A score that is computed and then never sorted on is not a ranking, it is a
// decoration. takeIDsCapped walks the slice in the order it is handed, so a
// builder that appends in pool order returns pool order, and the size cap then
// throws away the very tracks the score was picking out.
//
// The pools below are deliberately written worst-first, which is the shape the
// candidate pool really has: albums arrive newest first, and the newest album
// is rarely the most played.

func TestEssentialsTakesTheMostPlayedNotTheFirstFetched(t *testing.T) {
	tracks := []Track{
		{ID: "a", Artist: "A", PlayCount: 1},
		{ID: "b", Artist: "B", PlayCount: 2},
		{ID: "c", Artist: "C", PlayCount: 50},
	}

	got := BuildEssentials(tracks, 2, 3).TrackIDs

	if len(got) != 2 {
		t.Fatalf("got %d tracks, wanted 2", len(got))
	}
	if got[0] != "c" {
		t.Errorf("most played of all time opened on %q, wanted c (50 plays)", got[0])
	}
	for _, id := range got {
		if id == "a" {
			t.Errorf("the one play track made a two track chart: %v", got)
		}
	}
}

func TestOnRepeatLeadsWithWhatIsPlayedMost(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-24 * time.Hour)
	tracks := []Track{
		{ID: "a", Artist: "A", PlayCount: 2, LastPlayed: recent},
		{ID: "b", Artist: "B", PlayCount: 30, LastPlayed: recent},
	}

	got := BuildOnRepeat(tracks, now, 30*24*time.Hour, 10, 3).TrackIDs

	if len(got) == 0 || got[0] != "b" {
		t.Errorf("On Repeat opened on %v, wanted b (30 plays) first", got)
	}
}

// The anchor is the whole identity of a Daily Mix. A track that merely shares a
// genre with it scores a fraction of what the anchor's own tracks score, and
// still came first whenever the pool happened to hold it first.
func TestDailyMixOpensOnItsAnchorArtist(t *testing.T) {
	tracks := []Track{
		{ID: "neighbour", Artist: "Someone Else", Genre: "Rock", PlayCount: 1},
		{ID: "anchor", Artist: "The Anchor", Genre: "Rock", PlayCount: 1},
	}

	got := BuildDailyMix(tracks, []string{"The Anchor"}, 0, 10, 5).TrackIDs

	if len(got) == 0 || got[0] != "anchor" {
		t.Errorf("Daily Mix opened on %v, wanted the anchor artist first", got)
	}
}

func TestGenreRadioLeadsWithTheMostPlayedInThatGenre(t *testing.T) {
	tracks := []Track{
		{ID: "quiet", Artist: "A", Genre: "Jazz", PlayCount: 0},
		{ID: "loved", Artist: "B", Genre: "Jazz", PlayCount: 40},
	}

	got := BuildForGenre(tracks, "Jazz", 1, 3).TrackIDs

	if len(got) != 1 || got[0] != "loved" {
		t.Errorf("a one track Jazz radio picked %v, wanted the 40 play track", got)
	}
}

// New Music ranks on the date the file entered the library. Before, it ranked
// on position in the candidate pool, and the pool led with starred tracks.
func TestNewMusicRanksOnTheDateAdded(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	tracks := []Track{
		{ID: "starred-old", Artist: "A", Added: old, Starred: true},
		{ID: "added-yesterday", Artist: "B", Added: fresh},
	}

	got := BuildNewMusic(tracks, 10, 3).TrackIDs

	if len(got) == 0 || got[0] != "added-yesterday" {
		t.Errorf("New Music opened on %v, wanted the most recently added first", got)
	}
}

// A server that sends no date at all must behave exactly as it did before,
// which is the whole reason the zero time is treated as "unknown" rather than
// as 1970.
func TestNewMusicFallsBackToPoolOrderWithoutDates(t *testing.T) {
	tracks := []Track{
		{ID: "first", Artist: "A"},
		{ID: "second", Artist: "B"},
		{ID: "third", Artist: "C"},
	}

	got := BuildNewMusic(tracks, 10, 3).TrackIDs

	want := []string{"first", "second", "third"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("without dates New Music returned %v, wanted the pool order %v", got, want)
		}
	}
}

// Whatever the date says, something already played is not new to the listener.
func TestNewMusicStillPutsUnheardAheadOfARecentlyAddedFavourite(t *testing.T) {
	tracks := []Track{
		{ID: "played", Artist: "A", PlayCount: 9, Added: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)},
		{ID: "unheard", Artist: "B", Added: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}

	got := BuildNewMusic(tracks, 10, 3).TrackIDs

	if len(got) == 0 || got[0] != "unheard" {
		t.Errorf("New Music opened on %v, wanted the unheard track first", got)
	}
}
