package mixes

import (
	"strconv"
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

// #501721: a decade played hard but owned thinly used to win a rotation slot
// on plays alone and then come out under minMixSize, so the week showed one
// decade playlist instead of two. The floor ranks only decades a mix can fill.
func TestTopDecadesOwningSkipsADecadeThatCannotFillAMix(t *testing.T) {
	var tracks []Track
	// Three 1970s singles on heavy rotation: 303 points of plays, 3 tracks.
	for i := 0; i < 3; i++ {
		tracks = append(tracks, Track{ID: "70s" + string(rune('a'+i)), Artist: "Artist" + string(rune('a'+i)), Year: 1975, PlayCount: 100})
	}
	// Twelve 1990s tracks from six artists, played once each: 24 points.
	for i := 0; i < 12; i++ {
		tracks = append(tracks, Track{ID: "90s" + string(rune('a'+i)), Artist: "Band" + string(rune('a'+i%6)), Year: 1994, PlayCount: 1})
	}
	if got := TopDecades(tracks, 6); len(got) != 2 || got[0] != 1970 {
		t.Fatalf("TopDecades must keep ranking by plays, got %v", got)
	}
	got := TopDecadesOwning(tracks, 6, 10, 3)
	if len(got) != 1 || got[0] != 1990 {
		t.Errorf("TopDecadesOwning(minOwned 10) = %v, want only 1990: the 1970s hold 3 tracks and cannot fill a mix", got)
	}
}

// The floor counts what the per-artist cap would let a mix take, not raw
// tracks: twelve 1980s tracks by ONE artist are three under a cap of three.
func TestTopDecadesOwningAppliesThePerArtistCap(t *testing.T) {
	var tracks []Track
	for i := 0; i < 12; i++ {
		tracks = append(tracks, Track{ID: "80s" + string(rune('a'+i)), Artist: "Solo", Year: 1985, PlayCount: 5})
	}
	for i := 0; i < 12; i++ {
		tracks = append(tracks, Track{ID: "00s" + string(rune('a'+i)), Artist: "Band" + string(rune('a'+i%6)), Year: 2004, PlayCount: 0})
	}
	got := TopDecadesOwning(tracks, 6, 10, 3)
	if len(got) != 1 || got[0] != 2000 {
		t.Errorf("got %v, want only 2000: the 1980s are one artist capped at 3", got)
	}
	if got := TopDecadesOwning(tracks, 6, 10, 0); len(got) != 2 || got[0] != 1980 {
		t.Errorf("with no cap both decades qualify and plays rank first, got %v", got)
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

func TestWeekIndexIsStableWithinAWeekAndAdvancesBetweenWeeks(t *testing.T) {
	monday := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	sameWeek := monday.Add(3 * 24 * time.Hour)
	nextWeek := monday.Add(8 * 24 * time.Hour)

	if WeekIndex(monday) != WeekIndex(sameWeek) {
		t.Error("the rotation moved mid-week, so a pinned playlist would change under the user")
	}
	if WeekIndex(monday) == WeekIndex(nextWeek) {
		t.Error("the rotation did not advance after a week")
	}
}

// ISO week numbers reset each January, which would send the rotation
// backwards every new year.
func TestWeekIndexDoesNotResetAtTheYearBoundary(t *testing.T) {
	dec := time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)
	if WeekIndex(jan) <= WeekIndex(dec) {
		t.Errorf("rotation went backwards across the year: %d then %d", WeekIndex(dec), WeekIndex(jan))
	}
}

// The point of rotating is that a listener meets different music. A window
// equal to the pool would rotate forever and show the same set every week.
func TestRotateShowsADifferentWindowEachWeek(t *testing.T) {
	pool := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	first := Rotate(pool, 10, 3)
	second := Rotate(pool, 11, 3)
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("windows were %v and %v", first, second)
	}
	same := true
	for i := range first {
		if first[i] != second[i] {
			same = false
		}
	}
	if same {
		t.Errorf("consecutive weeks produced the same window: %v", first)
	}
}

func TestRotateIsStableForTheSameWeek(t *testing.T) {
	pool := []string{"a", "b", "c", "d", "e"}
	for i := 0; i < 5; i++ {
		if Rotate(pool, 42, 2)[0] != Rotate(pool, 42, 2)[0] {
			t.Fatal("the same week produced different windows")
		}
	}
}

func TestRotateSurvivesAPoolSmallerThanTheWindow(t *testing.T) {
	if got := Rotate([]string{"only"}, 7, 5); len(got) != 1 || got[0] != "only" {
		t.Errorf("Rotate = %v, want [only]", got)
	}
	if got := Rotate([]string{}, 7, 5); got != nil {
		t.Errorf("Rotate on an empty pool = %v, want nil", got)
	}
}

// SinTan1729 (issue #4): "The Artist Radio and Daily Mix playlist counters
// start at 2." The number is assigned from the loop index long before anyone
// knows whether the mix can be written, so any mix that comes out under
// minMixSize takes its number down with it. The four tests below cover the two
// ways a numbered mix came out too small.

// A mix whose description says it is built around an artist, and which then
// admits three tracks by that artist, contradicts itself and, at the default
// MaxPerArtist of 3, cannot even reach the ten tracks it needs to be written.
func TestDailyMixDoesNotThrottleItsOwnAnchor(t *testing.T) {
	// The anchor carries no genre, so nothing widens the mix and every track
	// in it has to come from the anchor itself. Measured before the fix: 3.
	var tracks []Track
	for i := 0; i < 40; i++ {
		tracks = append(tracks, Track{ID: "anchor" + strconv.Itoa(i), Artist: "Anchor", PlayCount: 100})
	}
	for i := 0; i < 60; i++ {
		tracks = append(tracks, Track{
			ID: "other" + strconv.Itoa(i), Artist: "Other" + strconv.Itoa(i%12),
			Genre: "Rock", PlayCount: 5,
		})
	}

	got := BuildDailyMix(tracks, []string{"Anchor"}, 0, 30, 3).TrackIDs

	if len(got) != 30 {
		t.Fatalf("a mix built around a 40 track anchor selected %d tracks, wanted 30: under minMixSize it is never written and its number goes missing", len(got))
	}
}

// The cap is not switched off, it simply stops applying to the anchor. It is
// still the thing that keeps one guest from taking over the mix.
func TestDailyMixStillCapsEveryArtistThatIsNotTheAnchor(t *testing.T) {
	// The anchor owns two tracks, so this mix genuinely has to widen through
	// the genre, which is where a hungry guest would otherwise eat it.
	tracks := []Track{
		{ID: "anchor0", Artist: "Anchor", Genre: "Rock", PlayCount: 100},
		{ID: "anchor1", Artist: "Anchor", Genre: "Rock", PlayCount: 100},
	}
	for i := 0; i < 40; i++ {
		tracks = append(tracks, Track{ID: "guest" + strconv.Itoa(i), Artist: "Guest", Genre: "Rock", PlayCount: 50})
	}
	for i := 0; i < 40; i++ {
		tracks = append(tracks, Track{
			ID: "filler" + strconv.Itoa(i), Artist: "Filler" + strconv.Itoa(i%10),
			Genre: "Rock", PlayCount: 1,
		})
	}

	got := BuildDailyMix(tracks, []string{"Anchor"}, 0, 30, 3).TrackIDs

	guests, anchors := 0, 0
	for _, id := range got {
		switch {
		case strings.HasPrefix(id, "guest"):
			guests++
		case strings.HasPrefix(id, "anchor"):
			anchors++
		}
	}
	if guests > 3 {
		t.Errorf("one guest artist contributed %d tracks against a cap of 3: exempting the anchor must not exempt everybody", guests)
	}
	if anchors != 2 {
		t.Errorf("the anchor contributed %d of its 2 tracks, wanted both: it is the artist the mix is named after", anchors)
	}
}

// ⛔ THE FILTER RUNS BEFORE THE TOP 20 IS TAKEN, NEVER AFTER. Rotate takes
// its weekly window at start = (week * size) % len(pool), so a pool that comes
// back short hands a different five artists to EVERY user on the server,
// including everyone whose numbering was never broken. Narrowing the eligible
// set first and then taking 20 is what keeps the length at 20.
func TestArtistPoolFiltersBeforeItTakesTheTop20(t *testing.T) {
	// Five one track artists with enormous play counts sit at the very top of
	// the ranking, which is the shape that produces the missing numbers, and
	// 25 artists behind them can each fill a mix.
	var tracks []Track
	for i := 0; i < 5; i++ {
		// TopArtists SUMS play counts, so these have to outrank the 1200 that
		// a 12 track artist accumulates, or they never reach the top 20 and
		// this test stops telling the two orderings apart.
		tracks = append(tracks, Track{ID: "solo" + strconv.Itoa(i), Artist: "Solo" + strconv.Itoa(i), PlayCount: 5000 + i})
	}
	for a := 0; a < 25; a++ {
		for i := 0; i < 12; i++ {
			tracks = append(tracks, Track{
				ID:        "a" + strconv.Itoa(a) + "-" + strconv.Itoa(i),
				Artist:    "Artist" + strconv.Itoa(100+(24-a)),
				PlayCount: 100 - a,
			})
		}
	}

	got := TopArtistsOwning(tracks, 20, 10)

	if len(got) != 20 {
		t.Fatalf("the pool came back %d long, wanted 20: filtering AFTER the cut throws away the slots the ineligible artists were occupying, and Rotate then moves the weekly window for every user on the server", len(got))
	}
	for _, a := range got {
		if strings.HasPrefix(a, "Solo") {
			t.Fatalf("a one track artist survived into %v: it is ranked, numbered, then skipped for being too small", got)
		}
	}
	// The survivors must still be ranked by plays, strongest first.
	for i := 0; i < 20; i++ {
		if want := "Artist" + strconv.Itoa(124-i); got[i] != want {
			t.Fatalf("position %d = %q, wanted %q: the pool is still ranked by plays, the floor only decides who is in it", i, got[i], want)
		}
	}
}

// The other half of the same promise: a user with no ineligible artists near
// the top must get back exactly what they get today, in the same order.
func TestArtistPoolLeavesAnUnaffectedUserExactlyWhereTheyWere(t *testing.T) {
	var tracks []Track
	for a := 0; a < 25; a++ {
		for i := 0; i < 12; i++ {
			tracks = append(tracks, Track{
				ID:        "a" + strconv.Itoa(a) + "-" + strconv.Itoa(i),
				Artist:    "Artist" + strconv.Itoa(100+(24-a)),
				PlayCount: 100 - a,
			})
		}
	}

	before := TopArtists(tracks, 20)
	after := TopArtistsOwning(tracks, 20, 10)

	if len(after) != len(before) {
		t.Fatalf("pool length went from %d to %d for a user whose artists all qualify", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("position %d changed from %q to %q: a user with no missing numbers must see no change whatsoever", i, before[i], after[i])
		}
	}
}

// The user the bug DOES touch: the top ranked artist owns one track, so it
// takes the number 1 with it and the counter starts at 2.
func TestArtistPoolDropsArtistsThatCouldNeverFillAMix(t *testing.T) {
	tracks := []Track{{ID: "solo", Artist: "Solo", PlayCount: 500}}
	for a := 0; a < 3; a++ {
		for i := 0; i < 12; i++ {
			tracks = append(tracks, Track{
				ID:        "r" + strconv.Itoa(a) + "-" + strconv.Itoa(i),
				Artist:    "Real" + strconv.Itoa(a),
				PlayCount: 10 - a,
			})
		}
	}

	if got := TopArtists(tracks, 20); got[0] != "Solo" {
		t.Fatalf("the unfiltered ranking opened on %q, this test needs the one track artist first", got[0])
	}

	got := TopArtistsOwning(tracks, 20, 10)

	for _, a := range got {
		if a == "Solo" {
			t.Errorf("a one track artist is still in the pool at %v: it is ranked, numbered, then skipped for being too small, and its number is the one the user cannot find", got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("pool = %v, wanted the 3 artists that can fill a mix", got)
	}
	for i, a := range got {
		if n := len(BuildForArtist(tracks, a, 30).TrackIDs); n < 10 {
			t.Errorf("Artist Radio %d (%s) would select %d tracks, under the 10 a mix needs, so its number would still be missing", i+1, a, n)
		}
	}
}

// ⛔ THE ELIGIBILITY COUNT MUST FOLD CASE, BECAUSE THE BUILDER DOES. Six "ABBA"
// plus six "Abba" is twelve tracks to BuildForArtist, which matches with
// EqualFold. Counting case sensitively would see two sixes, drop both, and
// DELETE an Artist Radio that works today, which is the opposite of the fix.
func TestArtistEligibilityCountsTheWayTheBuilderCollects(t *testing.T) {
	var tracks []Track
	for i := 0; i < 6; i++ {
		tracks = append(tracks, Track{ID: "upper" + strconv.Itoa(i), Artist: "ABBA", PlayCount: 9})
	}
	for i := 0; i < 6; i++ {
		tracks = append(tracks, Track{ID: "lower" + strconv.Itoa(i), Artist: "Abba", PlayCount: 9})
	}

	if n := len(BuildForArtist(tracks, "ABBA", 30).TrackIDs); n != 12 {
		t.Fatalf("BuildForArtist collected %d tracks, wanted all 12: the premise of this test is that it folds case", n)
	}

	if got := TopArtistsOwning(tracks, 20, 10); len(got) == 0 {
		t.Error("an artist spelled two ways was dropped from the pool although the builder finds all 12 of its tracks, so a working Artist Radio would disappear")
	}
}

// The old signature keeps its old meaning: no floor, so a one track artist is
// still ranked. Every existing caller and every existing test depends on this.
func TestTopArtistsStillRanksEveryoneWithAPlay(t *testing.T) {
	tracks := []Track{
		{ID: "s", Artist: "Solo", PlayCount: 500},
		{ID: "d1", Artist: "Duo", PlayCount: 3},
		{ID: "d2", Artist: "Duo", PlayCount: 3},
		{ID: "n", Artist: "NeverPlayed"},
	}

	got := TopArtists(tracks, 20)

	if len(got) != 2 || got[0] != "Solo" || got[1] != "Duo" {
		t.Errorf("TopArtists = %v, wanted [Solo Duo]: delegating with a floor of 0 must change nothing", got)
	}
}

// GENRE RADIO CARRIES THE SAME DEFECT AS ARTIST RADIO and had to be fixed in
// the same release: leaving two of the three numbered families fixed and the
// third broken is a difference we would have introduced ourselves, and the
// person who reported it would have met it again the following week.

// ⛔ SAME ORDERING RULE AS THE ARTIST POOL. Rotate takes its window at
// start = (week * size) % len(pool), so the eligible set has to be narrowed
// before the top 12 is taken, never after.
func TestGenrePoolFiltersBeforeItTakesTheTop12(t *testing.T) {
	var tracks []Track
	// Five genres carried by ONE heavily played track each. TopGenres scores
	// a genre at PlayCount+1 per track, so these outrank the 1414 that a real
	// 14 track genre accumulates and sit at the very top of the ranking.
	for i := 0; i < 5; i++ {
		tracks = append(tracks, Track{ID: "thin" + strconv.Itoa(i), Artist: "A" + strconv.Itoa(i),
			Genre: "Thin" + strconv.Itoa(i), PlayCount: 5000})
	}
	for x := 0; x < 15; x++ {
		for i := 0; i < 14; i++ {
			tracks = append(tracks, Track{
				ID:     "g" + strconv.Itoa(x) + "-" + strconv.Itoa(i),
				Artist: "Band" + strconv.Itoa(i%5),
				Genre:  "Genre" + strconv.Itoa(100+(14-x)), PlayCount: 100 - x,
			})
		}
	}

	got := TopGenresOwning(tracks, 12, 10, 3)

	if len(got) != 12 {
		t.Fatalf("the pool came back %d long, wanted 12: filtering AFTER the cut throws away the slots the one track genres were occupying, and Rotate then moves the weekly window for every user on the server", len(got))
	}
	for _, g := range got {
		if strings.HasPrefix(g, "Thin") {
			t.Fatalf("a one track genre survived into %v: it is ranked, numbered, then skipped for being too small, and Genre Radio starts at 2", got)
		}
	}
	for i := 0; i < 12; i++ {
		if want := "Genre" + strconv.Itoa(114-i); got[i] != want {
			t.Fatalf("position %d = %q, wanted %q: the pool is still ranked by plays, the floor only decides who is in it", i, got[i], want)
		}
	}
}

// The other half of the promise, exactly as for artists: a user with no thin
// genres near the top must get back what they get today, in the same order.
func TestGenrePoolLeavesAnUnaffectedUserExactlyWhereTheyWere(t *testing.T) {
	var tracks []Track
	for x := 0; x < 15; x++ {
		for i := 0; i < 14; i++ {
			tracks = append(tracks, Track{
				ID:     "g" + strconv.Itoa(x) + "-" + strconv.Itoa(i),
				Artist: "Band" + strconv.Itoa(i%5),
				Genre:  "Genre" + strconv.Itoa(100+(14-x)), PlayCount: 100 - x,
			})
		}
	}

	before := TopGenres(tracks, 12)
	after := TopGenresOwning(tracks, 12, 10, 3)

	if len(after) != len(before) {
		t.Fatalf("pool length went from %d to %d for a user whose genres all qualify", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("position %d changed from %q to %q: a user with no missing numbers must see no change whatsoever", i, before[i], after[i])
		}
	}
}

// ⛔ THE DIFFERENCE ARTISTS DO NOT HAVE: A TRACK HAS MORE THAN ONE GENRE.
// BuildForGenre collects with HasGenre, which is EqualFold over AllGenres, so a
// track tagged "Hip-Hop; Funk" is a Funk track AND a Hip-Hop track and has to
// count for both. Counting the legacy single Genre field instead would score
// Funk at zero on a library full of funk, which is issue #1 from Sly777.
func TestGenreEligibilityCountsSecondaryGenresTheWayTheBuilderDoes(t *testing.T) {
	var tracks []Track
	for i := 0; i < 12; i++ {
		tracks = append(tracks, Track{
			ID: "hf" + strconv.Itoa(i), Artist: "Band" + strconv.Itoa(i%4),
			Genres: []string{"Hip-Hop", "Funk"}, PlayCount: 4,
		})
	}

	if n := len(BuildForGenre(tracks, "Funk", 30, 3).TrackIDs); n != 12 {
		t.Fatalf("BuildForGenre collected %d Funk tracks, wanted all 12: the premise of this test is that a second genre is a real genre", n)
	}

	got := TopGenresOwning(tracks, 12, 10, 3)

	var sawFunk bool
	for _, g := range got {
		if strings.EqualFold(g, "Funk") {
			sawFunk = true
		}
	}
	if !sawFunk {
		t.Errorf("Funk was dropped from %v although the builder finds all 12 of its tracks: the eligibility count must read every genre a track carries, not the legacy first one", got)
	}
}

// The old signature keeps its old meaning: no floor, so a genre carried by one
// track is still ranked. Every existing caller depends on this.
//
// "Common" here is 12 tracks by a SINGLE artist, so its capacity under a cap of
// 3 is only 3. That is deliberate: a floor of 0 has to pass everyone whatever
// the cap says, which is the second half of keeping this change additive.
func TestTopGenresStillRanksEveryGenreItSees(t *testing.T) {
	tracks := []Track{{ID: "one", Artist: "A", Genre: "Rare", PlayCount: 500}}
	for i := 0; i < 12; i++ {
		tracks = append(tracks, Track{ID: "c" + strconv.Itoa(i), Artist: "B", Genre: "Common", PlayCount: 1})
	}

	got := TopGenres(tracks, 12)

	if len(got) != 2 || got[0] != "Rare" || got[1] != "Common" {
		t.Errorf("TopGenres = %v, wanted [Rare Common]: delegating with a floor of 0 must change nothing", got)
	}
	withCap := TopGenresOwning(tracks, 12, 0, 3)
	if len(withCap) != len(got) {
		t.Errorf("a floor of 0 with a cap of 3 returned %v, wanted the same %v: zero means everybody passes, whatever the cap", withCap, got)
	}
}

// ⛔ COUNTING TRACKS IS STILL THE WRONG NUMBER, AND THIS IS THE MEASUREMENT
// THAT SAYS SO. BuildForGenre hands its candidates to takeIDsCapped WITH
// maxPerArtist, which BuildForArtist does not, so a genre needs
// ceil(minMixSize / maxPerArtist) distinct artists however many tracks it owns.
// A genre with 20 tracks by 2 artists clears any floor that counts tracks and
// then yields six, and Genre Radio still starts at 2.
func TestGenreEligibilityMeasuresWhatTheCapWillLetThrough(t *testing.T) {
	var tracks []Track
	for i := 0; i < 20; i++ {
		tracks = append(tracks, Track{ID: "duo" + strconv.Itoa(i),
			Artist: "Duo" + strconv.Itoa(i%2), Genre: "TwoArtistGenre", PlayCount: 90})
	}
	for i := 0; i < 20; i++ {
		tracks = append(tracks, Track{ID: "wide" + strconv.Itoa(i),
			Artist: "Band" + strconv.Itoa(i%7), Genre: "SevenArtistGenre", PlayCount: 5})
	}

	// The premise: the builder really does come back with six.
	if n := len(BuildForGenre(tracks, "TwoArtistGenre", 30, 3).TrackIDs); n != 6 {
		t.Fatalf("BuildForGenre returned %d tracks for a 20 track genre with 2 artists, wanted 6: this test exists because the cap, not the track count, decides", n)
	}

	got := TopGenresOwning(tracks, 12, 10, 3)

	for _, g := range got {
		if g == "TwoArtistGenre" {
			t.Errorf("a genre that can only ever yield 6 tracks is in the pool at %v: it gets a number it cannot use and Genre Radio starts at 2", got)
		}
	}
	if len(got) != 1 || got[0] != "SevenArtistGenre" {
		t.Fatalf("pool = %v, wanted only the genre with enough artists to fill a mix", got)
	}
}

// takeIDsCapped never caps a track whose artist is blank, because its per
// artist test is guarded by key != "". The capacity count has to mirror that or
// a genre of untagged tracks would be under-counted and thrown out of a pool it
// can perfectly well fill.
func TestGenreCapacityDoesNotCapBlankArtistsBecauseTheBuilderDoesNot(t *testing.T) {
	var tracks []Track
	for i := 0; i < 12; i++ {
		tracks = append(tracks, Track{ID: "u" + strconv.Itoa(i), Artist: "", Genre: "Untagged", PlayCount: 4})
	}

	if n := len(BuildForGenre(tracks, "Untagged", 30, 3).TrackIDs); n != 12 {
		t.Fatalf("BuildForGenre returned %d of 12 blank artist tracks, wanted all of them: the cap does not apply to an empty artist key", n)
	}

	got := TopGenresOwning(tracks, 12, 10, 3)

	if len(got) != 1 || got[0] != "Untagged" {
		t.Errorf("pool = %v, wanted [Untagged]: the floor capped a blank artist that the builder will not cap, so it rejected a genre that can fill a mix", got)
	}
}

// The other direction, so the floor cannot simply reject everything: the SAME
// 20 tracks spread over enough artists still qualifies, exactly as before.
func TestAGenreWithEnoughArtistsStillQualifiesAsItDidBefore(t *testing.T) {
	var tracks []Track
	for i := 0; i < 20; i++ {
		tracks = append(tracks, Track{ID: "w" + strconv.Itoa(i),
			Artist: "Band" + strconv.Itoa(i%7), Genre: "Wide", PlayCount: 5})
	}

	// 7 artists at a cap of 3 is a capacity of 20, so nothing is withheld.
	if n := len(BuildForGenre(tracks, "Wide", 30, 3).TrackIDs); n != 20 {
		t.Fatalf("BuildForGenre returned %d of 20 tracks, wanted all of them: the cap does not bind at this artist spread", n)
	}

	before := TopGenres(tracks, 12)
	after := TopGenresOwning(tracks, 12, 10, 3)

	if len(after) != len(before) {
		t.Fatalf("pool went from %v to %v: a genre with enough artists must pass exactly as it did before", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("position %d changed from %q to %q", i, before[i], after[i])
		}
	}
}

// ⛔ THE THIRD ROUTE TO A MISSING NUMBER, AND THE ONLY ONE THAT IS NOT ABOUT
// SIZE AT ALL. A tag editor that leaves a leading space on half an artist's
// tracks is ordinary. BuildForArtist trims before it compares (EqualFold over
// TrimSpace) and sees all twelve, and TopArtistsOwning counts trimmed too, so
// Artist Radio is perfectly healthy. BuildDailyMix compared WITHOUT trimming,
// so it saw six. Measured on this exact library: Artist Radio 12, Daily Mix 9.
//
// The anchor here deliberately carries NO genre, which is what makes this test
// catch both halves of the fix. Untrimmed scoring cannot widen through a genre
// that does not exist, so it selects six. Untrimmed exemption scores all twelve
// but then lets the per artist cap throw six of them away, so it selects nine.
func TestDailyMixRecognisesItsAnchorThroughRaggedArtistTags(t *testing.T) {
	var tracks []Track
	for i := 0; i < 12; i++ {
		name := "Padded"
		if i%2 == 0 {
			name = " Padded" // half the tracks carry a leading space
		}
		tracks = append(tracks, Track{ID: "p" + strconv.Itoa(i), Artist: name, PlayCount: 900})
	}
	for a := 0; a < 4; a++ {
		for i := 0; i < 12; i++ {
			tracks = append(tracks, Track{ID: "o" + strconv.Itoa(a) + "-" + strconv.Itoa(i),
				Artist: "Other" + strconv.Itoa(a), Genre: "Jazz", PlayCount: 3})
		}
	}

	pool := TopArtistsOwning(tracks, 20, 10)
	if len(pool) == 0 || pool[0] != "Padded" {
		t.Fatalf("pool = %v, this test needs the raggedly tagged artist ranked first", pool)
	}
	// Artist Radio is fine on this library, which is what makes the Daily Mix
	// hole so hard to see from the outside.
	if n := len(BuildForArtist(tracks, pool[0], 30).TrackIDs); n != 12 {
		t.Fatalf("Artist Radio selected %d tracks, wanted 12: BuildForArtist trims, and that is the behaviour Daily Mix has to match", n)
	}

	got := BuildDailyMix(tracks, pool, 0, 30, 3).TrackIDs

	anchors := 0
	for _, id := range got {
		if strings.HasPrefix(id, "p") {
			anchors++
		}
	}
	if anchors != 12 {
		t.Errorf("the mix took %d of the anchor's 12 tracks: a leading space made half of them look like a different artist", anchors)
	}
	if len(got) < 10 {
		t.Errorf("Daily Mix selected %d tracks, under the 10 a mix needs, so its number goes missing on a library where Artist Radio is perfectly healthy", len(got))
	}
}

// Steven O'Neil asked whether New Music was the release date or the date the
// file was added. The default is, and stays, the added date: this pins that
// BuildNewMusic and BuildNewMusicBy with "added" give the same answer, so an
// upgrade cannot reorder anybody's pinned playlist.
func TestNewMusicDefaultOrderIsUnchangedByTheReleasedOption(t *testing.T) {
	tracks := []Track{
		{ID: "old-record-added-today", Added: ago(1 * day), Released: date(1984, 6, 1)},
		{ID: "new-record-added-last-month", Added: ago(30 * day), Released: date(2026, 8, 1)},
		{ID: "undated", Added: ago(10 * day)},
	}
	want := []string{"old-record-added-today", "undated", "new-record-added-last-month"}
	for _, got := range []Selection{
		BuildNewMusic(tracks, 10, 5),
		BuildNewMusicBy(tracks, NewMusicByAdded, 10, 5),
		// An order nobody defined is not an order: it must fall through to
		// the default rather than to anything else.
		BuildNewMusicBy(tracks, NewMusicOrder("typo"), 10, 5),
	} {
		if strings.Join(got.TrackIDs, ",") != strings.Join(want, ",") {
			t.Errorf("selected %v, want the added order %v", got.TrackIDs, want)
		}
	}
}

// In released order the newest RELEASE leads, whatever day the file arrived,
// and a track with no release date at all sorts behind every dated one.
func TestNewMusicByReleasedRanksOnTheReleaseDate(t *testing.T) {
	tracks := []Track{
		{ID: "old-record-added-today", Added: ago(1 * day), Released: date(1984, 6, 1)},
		{ID: "undated-added-today", Added: ago(1 * day)},
		{ID: "new-record-added-last-month", Added: ago(30 * day), Released: date(2026, 8, 1)},
		{ID: "newer-record-added-last-year", Added: ago(400 * day), Released: date(2026, 8, 15)},
	}
	got := BuildNewMusicBy(tracks, NewMusicByReleased, 10, 5)
	want := []string{"newer-record-added-last-year", "new-record-added-last-month", "old-record-added-today", "undated-added-today"}
	if strings.Join(got.TrackIDs, ",") != strings.Join(want, ",") {
		t.Errorf("selected %v, want %v", got.TrackIDs, want)
	}
}

// Mixed precision is a property of the tags and it is documented, not hidden:
// a year-only date is 1 January, so a full date later in that same year
// outranks it, and a full date in an EARLIER year never does.
func TestNewMusicByReleasedOrdersMixedPrecisionAsDocumented(t *testing.T) {
	tracks := []Track{
		{ID: "year-only-2025", Released: date(2025, 1, 1)},
		{ID: "full-date-2025", Released: date(2025, 3, 1)},
		{ID: "full-date-2024", Released: date(2024, 12, 31)},
	}
	got := BuildNewMusicBy(tracks, NewMusicByReleased, 10, 5)
	want := []string{"full-date-2025", "year-only-2025", "full-date-2024"}
	if strings.Join(got.TrackIDs, ",") != strings.Join(want, ",") {
		t.Errorf("selected %v, want %v", got.TrackIDs, want)
	}
}

// Two tracks released the same day are broken by the added date, newest
// arrival first, so a library tagged with years only still moves with what
// arrived this week. Position stays the last resort.
func TestNewMusicByReleasedBreaksTiesOnTheAddedDateThenPosition(t *testing.T) {
	tracks := []Track{
		{ID: "first-in-pool", Released: date(2026, 1, 1), Added: ago(30 * day)},
		{ID: "second-in-pool", Released: date(2026, 1, 1), Added: ago(30 * day)},
		{ID: "arrived-yesterday", Released: date(2026, 1, 1), Added: ago(1 * day)},
	}
	got := BuildNewMusicBy(tracks, NewMusicByReleased, 10, 5)
	want := []string{"arrived-yesterday", "first-in-pool", "second-in-pool"}
	if strings.Join(got.TrackIDs, ",") != strings.Join(want, ",") {
		t.Errorf("selected %v, want %v", got.TrackIDs, want)
	}
}

// Sly777's rule survives the new order: something already played goes behind
// everything unheard, even when it is the newest release in the pool.
func TestNewMusicByReleasedStillPushesPlayedTracksBehindUnplayed(t *testing.T) {
	tracks := []Track{
		{ID: "played-newest", PlayCount: 4, Released: date(2026, 8, 1)},
		{ID: "unplayed-older", Released: date(2020, 1, 1)},
	}
	got := BuildNewMusicBy(tracks, NewMusicByReleased, 10, 5)
	if len(got.TrackIDs) != 2 || got.TrackIDs[0] != "unplayed-older" {
		t.Errorf("selected %v, want the unplayed track first", got.TrackIDs)
	}
}

// A library with no dates at all comes out in exactly the added order, so the
// released option can never make a mix worse than the default.
func TestNewMusicByReleasedFallsBackToTheAddedOrderWithoutDates(t *testing.T) {
	tracks := []Track{
		{ID: "oldest", Added: ago(30 * day)},
		{ID: "newest", Added: ago(1 * day)},
		{ID: "middle", Added: ago(10 * day)},
	}
	added := BuildNewMusicBy(tracks, NewMusicByAdded, 10, 5)
	released := BuildNewMusicBy(tracks, NewMusicByReleased, 10, 5)
	if strings.Join(added.TrackIDs, ",") != strings.Join(released.TrackIDs, ",") {
		t.Errorf("released order %v differs from added order %v on an undated library", released.TrackIDs, added.TrackIDs)
	}
	if released.TrackIDs[0] != "newest" {
		t.Errorf("selected %v, want newest addition first", released.TrackIDs)
	}
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
