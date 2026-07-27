package mixes

import (
	"strings"
	"testing"
	"time"
)

// THE RULE THAT MAKES THESE DIFFERENT from the plugin they replace: a playlist
// name never contains variable data. A name like "Artist Radio 1: LINKIN PARK"
// changes the day the rankings shift, which creates a NEW playlist and orphans
// the old one, which is why servers running such a plugin need a cleanup job on
// a cron. The slot key must be stable and carry only its position.
func TestNumberedSlotsCarryNoVariableData(t *testing.T) {
	slot := NumberedSlot(GenreRadio, 2)
	if slot != "genreradio-2" {
		t.Fatalf("NumberedSlot = %q", slot)
	}
	// Whatever genre is second this week, the identity is unchanged.
	if NumberedSlot(GenreRadio, 2) != slot {
		t.Error("the same position produced a different identity")
	}
	for _, forbidden := range []string{"rock", "linkin", "hard"} {
		if strings.Contains(strings.ToLower(slot), forbidden) {
			t.Errorf("slot key leaked content: %q", slot)
		}
	}
}

func TestOnRepeatWantsRecentAndRepeatedNotJustPopular(t *testing.T) {
	tracks := []Track{
		{ID: "current", PlayCount: 8, LastPlayed: ago(3 * day)},
		{ID: "old-hit", PlayCount: 400, LastPlayed: ago(300 * day)},
		{ID: "recent-once", PlayCount: 1, LastPlayed: ago(1 * day)},
	}
	got := BuildOnRepeat(tracks, now, 30*day, 10, 3)
	if len(got.TrackIDs) != 1 || got.TrackIDs[0] != "current" {
		t.Errorf("selected %v, want only current. An all-time hit nobody plays now is Essentials, not On Repeat", got.TrackIDs)
	}
}

func TestEssentialsIsAllTimeAndIgnoresRecency(t *testing.T) {
	tracks := []Track{
		{ID: "old-hit", PlayCount: 400, LastPlayed: ago(900 * day)},
		{ID: "never", PlayCount: 0},
	}
	got := BuildEssentials(tracks, 10, 3)
	if len(got.TrackIDs) != 1 || got.TrackIDs[0] != "old-hit" {
		t.Errorf("selected %v, want old-hit", got.TrackIDs)
	}
}

// A library is full of tracks that arrived with an album and were never
// anyone's choice. Those are not discoveries, they are filler.
func TestDiscoverySkipsNeverPlayedFiller(t *testing.T) {
	tracks := []Track{
		{ID: "filler", PlayCount: 0},
		{ID: "forgotten", PlayCount: 2, LastPlayed: ago(200 * day)},
		{ID: "favourite", PlayCount: 2, Starred: true, LastPlayed: ago(200 * day)},
		{ID: "heavy", PlayCount: 50, LastPlayed: ago(200 * day)},
	}
	got := BuildDiscovery(tracks, now, 90*day, 10, 3)
	if len(got.TrackIDs) != 1 || got.TrackIDs[0] != "forgotten" {
		t.Errorf("selected %v, want only forgotten", got.TrackIDs)
	}
}

// A genre with thousands of untouched files is a shelf, not a taste.
func TestTopGenresRankByPlaysNotByFileCount(t *testing.T) {
	var tracks []Track
	for i := 0; i < 500; i++ {
		tracks = append(tracks, Track{ID: "shelf" + string(rune(i)), Genre: "Field Recordings"})
	}
	for i := 0; i < 20; i++ {
		tracks = append(tracks, Track{ID: "loved" + string(rune(i)), Genre: "Hard Rock", PlayCount: 60})
	}
	got := TopGenres(tracks, 2)
	if len(got) == 0 || got[0] != "Hard Rock" {
		t.Errorf("TopGenres = %v, want Hard Rock first", got)
	}
}

func TestArtistRadioIsOneArtistAndSaysSo(t *testing.T) {
	tracks := []Track{
		{ID: "a1", Artist: "LINKIN PARK", PlayCount: 5},
		{ID: "a2", Artist: "LINKIN PARK", PlayCount: 3},
		{ID: "b1", Artist: "Someone Else", PlayCount: 90},
	}
	got := BuildForArtist(tracks, "LINKIN PARK", 10)
	if len(got.TrackIDs) != 2 {
		t.Fatalf("selected %v, want both LINKIN PARK tracks and nothing else", got.TrackIDs)
	}
	for _, id := range got.TrackIDs {
		if id == "b1" {
			t.Error("another artist leaked into an artist radio")
		}
	}
}

func TestDecadeBucketsYears(t *testing.T) {
	for year, want := range map[int]int{1994: 1990, 2000: 2000, 2009: 2000, 2026: 2020} {
		if got := Decade(year); got != want {
			t.Errorf("Decade(%d) = %d, want %d", year, got, want)
		}
	}
}

func TestDecadeMixOnlyTakesThatDecade(t *testing.T) {
	tracks := []Track{
		{ID: "90s", Year: 1994, PlayCount: 2},
		{ID: "00s", Year: 2004, PlayCount: 9},
		{ID: "untagged", Year: 0, PlayCount: 9},
	}
	got := BuildForDecade(tracks, 1990, 10, 3)
	if len(got.TrackIDs) != 1 || got.TrackIDs[0] != "90s" {
		t.Errorf("selected %v, want only the 1994 track", got.TrackIDs)
	}
}

// Three Daily Mixes anchored on the same artist would be one mix printed three
// times, which is what makes the feature feel fake.
func TestDailyMixesAreAnchoredOnDifferentArtists(t *testing.T) {
	tracks := []Track{
		{ID: "a1", Artist: "A", Genre: "Rock", PlayCount: 10},
		{ID: "a2", Artist: "A", Genre: "Rock", PlayCount: 8},
		{ID: "b1", Artist: "B", Genre: "Jazz", PlayCount: 9},
		{ID: "b2", Artist: "B", Genre: "Jazz", PlayCount: 7},
	}
	anchors := []string{"A", "B"}
	one := BuildDailyMix(tracks, anchors, 0, 10, 5)
	two := BuildDailyMix(tracks, anchors, 1, 10, 5)
	if len(one.TrackIDs) == 0 || len(two.TrackIDs) == 0 {
		t.Fatal("a daily mix came out empty")
	}
	if one.TrackIDs[0] == two.TrackIDs[0] {
		t.Errorf("both mixes lead with %q, so they are the same mix twice", one.TrackIDs[0])
	}
}

func TestDailyMixIsEmptyWhenThereIsNoAnchor(t *testing.T) {
	got := BuildDailyMix([]Track{{ID: "x", Artist: "A"}}, nil, 0, 10, 3)
	if len(got.TrackIDs) != 0 {
		t.Errorf("selected %v with no anchors", got.TrackIDs)
	}
}

func TestLovedSongsLeadsWithTheOldestFavourite(t *testing.T) {
	tracks := []Track{
		{ID: "new-fav", Starred: true, LastPlayed: ago(1 * day)},
		{ID: "old-fav", Starred: true, LastPlayed: ago(500 * day)},
		{ID: "not-fav", PlayCount: 99, LastPlayed: ago(500 * day)},
	}
	got := BuildLovedSongs(tracks, 10, 5)
	if len(got.TrackIDs) != 2 || got.TrackIDs[0] != "old-fav" {
		t.Errorf("selected %v, want the oldest favourite first and nothing unstarred", got.TrackIDs)
	}
}

func TestNewMusicKeepsTheOrderItWasGiven(t *testing.T) {
	tracks := []Track{{ID: "newest"}, {ID: "middle"}, {ID: "oldest"}}
	got := BuildNewMusic(tracks, 10, 5)
	if len(got.TrackIDs) != 3 || got.TrackIDs[0] != "newest" {
		t.Errorf("selected %v, want newest first", got.TrackIDs)
	}
}

var _ = time.Now
