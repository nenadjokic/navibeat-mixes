package mixes

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

const day = 24 * time.Hour

func TestSlotForHourCoversEveryHourAndWrapsMidnight(t *testing.T) {
	want := map[int]Slot{
		0: Night, 4: Night, 5: Morning, 10: Morning,
		11: Afternoon, 16: Afternoon, 17: Evening, 22: Evening, 23: Night,
	}
	for hour, expected := range want {
		if got := SlotForHour(hour); got != expected {
			t.Errorf("SlotForHour(%d) = %q, want %q", hour, got, expected)
		}
	}
	// Every hour must land somewhere, or a play would be silently dropped.
	for h := 0; h < 24; h++ {
		if SlotForHour(h) == "" {
			t.Errorf("hour %d maps to no slot", h)
		}
	}
}

func rediscoverOpts() RediscoverOptions {
	return RediscoverOptions{
		Now: now, MinAge: 180 * day, RecentGrace: 30 * day, MinPlayCount: 3, Size: 10,
	}
}

func TestRediscoverPicksOldFavouritesAndNothingElse(t *testing.T) {
	tracks := []Track{
		{ID: "old-starred", Starred: true, LastPlayed: ago(300 * day)},
		{ID: "old-played", PlayCount: 5, LastPlayed: ago(240 * day)},
		{ID: "old-but-ignored", PlayCount: 1, LastPlayed: ago(300 * day)},
		{ID: "loved-but-recent", Starred: true, LastPlayed: ago(10 * day)},
		{ID: "never-played", Starred: true},
	}
	got := BuildRediscover(tracks, rediscoverOpts())

	if got.Slot != Rediscover {
		t.Errorf("slot = %q, want %q", got.Slot, Rediscover)
	}
	want := []string{"old-starred", "old-played"}
	if len(got.TrackIDs) != len(want) {
		t.Fatalf("selected %v, want %v", got.TrackIDs, want)
	}
	for i := range want {
		if got.TrackIDs[i] != want[i] {
			t.Fatalf("selected %v, want %v (oldest first)", got.TrackIDs, want)
		}
	}
}

// A track the user has only just come back to must not be handed straight
// back to them as a rediscovery. This is the rule that keeps the mix feeling
// like it knows something.
func TestRediscoverExcludesRecentlyPlayed(t *testing.T) {
	tracks := []Track{{ID: "just-played", Starred: true, LastPlayed: ago(2 * day)}}
	if got := BuildRediscover(tracks, rediscoverOpts()); len(got.TrackIDs) != 0 {
		t.Errorf("selected %v, want nothing", got.TrackIDs)
	}
}

func TestRediscoverRespectsSize(t *testing.T) {
	var tracks []Track
	for i := 0; i < 50; i++ {
		tracks = append(tracks, Track{ID: string(rune('a'+i%26)) + string(rune('0'+i/26)), Starred: true, LastPlayed: ago(300 * day)})
	}
	opt := rediscoverOpts()
	opt.Size = 7
	if got := BuildRediscover(tracks, opt); len(got.TrackIDs) != 7 {
		t.Errorf("selected %d tracks, want 7", len(got.TrackIDs))
	}
}

// Below the evidence threshold the mix must say fallback. Claiming affinity
// without the data behind it is the one thing that would make the feature
// feel dishonest.
func TestTimeMixFallsBackBelowTheEventThreshold(t *testing.T) {
	tracks := []Track{
		{ID: "popular", PlayCount: 9},
		{ID: "loved", PlayCount: 1, Starred: true},
		{ID: "unplayed"},
	}
	got := BuildTimeMix(tracks, TimeMixOptions{
		Slot: Morning, EventCount: 10, MinEventsForAffinity: 150, Size: 10,
	})
	if got.Mode != ModeFallback {
		t.Fatalf("mode = %q, want %q", got.Mode, ModeFallback)
	}
	if len(got.TrackIDs) == 0 || got.TrackIDs[0] != "popular" {
		t.Errorf("selected %v, want the most played first", got.TrackIDs)
	}
	for _, id := range got.TrackIDs {
		if id == "unplayed" {
			t.Error("a never-played track with no signal should not appear")
		}
	}
}

func TestTimeMixUsesAffinityOnceThereIsEnoughEvidence(t *testing.T) {
	tracks := []Track{
		{ID: "morning-track", PlayCount: 2},
		{ID: "night-track", PlayCount: 50},
	}
	aff := SlotAffinity{
		"morning-track": {Morning: 8},
		"night-track":   {Night: 40},
	}
	got := BuildTimeMix(tracks, TimeMixOptions{
		Slot: Morning, Affinity: aff, EventCount: 200, MinEventsForAffinity: 150, Size: 10,
	})
	if got.Mode != ModeAffinity {
		t.Fatalf("mode = %q, want %q", got.Mode, ModeAffinity)
	}
	// The far more popular track belongs to a different slot and must lose,
	// otherwise the mix is just a chart with a time-of-day label on it.
	if len(got.TrackIDs) != 1 || got.TrackIDs[0] != "morning-track" {
		t.Errorf("selected %v, want only morning-track", got.TrackIDs)
	}
}

func TestTimeMixCapsOneArtistFillingTheMix(t *testing.T) {
	var tracks []Track
	for i := 0; i < 20; i++ {
		tracks = append(tracks, Track{
			ID: "hog-" + string(rune('a'+i)), Artist: "Prolific", PlayCount: 100 - i,
		})
	}
	tracks = append(tracks, Track{ID: "other", Artist: "Someone Else", PlayCount: 1})

	got := BuildTimeMix(tracks, TimeMixOptions{
		Slot: Evening, EventCount: 0, MinEventsForAffinity: 150, Size: 10, MaxPerArtist: 3,
	})
	hogs := 0
	for _, id := range got.TrackIDs {
		if len(id) > 4 && id[:4] == "hog-" {
			hogs++
		}
	}
	if hogs > 3 {
		t.Errorf("one artist contributed %d tracks, cap was 3 (%v)", hogs, got.TrackIDs)
	}
}

// The real case this exists for: a tagging tool that wrote one genre onto
// almost the whole library. Selecting on it looks deliberate and is random.
func TestFilterGenresNeutralisesAGenreThatCoversTheLibrary(t *testing.T) {
	var tracks []Track
	for i := 0; i < 99; i++ {
		tracks = append(tracks, Track{ID: "t" + string(rune(i)), Genre: "Music"})
	}
	tracks = append(tracks, Track{ID: "real", Genre: "Jazz"})

	got := FilterGenres(tracks, nil, 0.6)
	if len(got) != len(tracks) {
		t.Fatalf("dropped tracks: got %d, want %d. Filtering must neutralise the genre, not delete the library", len(got), len(tracks))
	}
	for _, tr := range got {
		if tr.ID == "real" && tr.Genre != "Jazz" {
			t.Error("a genuinely discriminating genre was cleared")
		}
		if tr.ID != "real" && tr.Genre != "" {
			t.Errorf("noise genre survived on %s: %q", tr.ID, tr.Genre)
		}
	}
}

func TestFilterGenresHonoursTheDenylistCaseInsensitively(t *testing.T) {
	tracks := []Track{{ID: "a", Genre: "music"}, {ID: "b", Genre: "Rock"}}
	got := FilterGenres(tracks, []string{"Music"}, 0.9)
	if got[0].Genre != "" {
		t.Errorf("denylisted genre survived: %q", got[0].Genre)
	}
	if got[1].Genre != "Rock" {
		t.Errorf("unrelated genre was cleared: %q", got[1].Genre)
	}
}

// Selection must not wobble between runs, or every scheduled run rewrites the
// playlist and it looks like it changed when nothing did.
func TestSelectionIsDeterministic(t *testing.T) {
	tracks := []Track{
		{ID: "b", PlayCount: 5}, {ID: "a", PlayCount: 5}, {ID: "c", PlayCount: 5},
	}
	opt := TimeMixOptions{Slot: Night, EventCount: 0, MinEventsForAffinity: 150, Size: 3}
	first := BuildTimeMix(tracks, opt).TrackIDs
	for i := 0; i < 5; i++ {
		again := BuildTimeMix(tracks, opt).TrackIDs
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("run %d differed: %v vs %v", i, again, first)
			}
		}
	}
}

// The bug this was written for, measured on a real library: 249 starred
// tracks, the OLDEST last played 100 days ago, a six month default, and
// therefore no playlist at all with nothing on screen to explain why.
func TestRediscoverWidensItsWindowRatherThanProducingNothing(t *testing.T) {
	var tracks []Track
	for i := 0; i < 40; i++ {
		tracks = append(tracks, Track{
			ID:      "t" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Starred: true, LastPlayed: ago(time.Duration(60+i) * day),
		})
	}
	opt := RediscoverOptions{
		Now: now, MinAge: 180 * day, RecentGrace: 30 * day,
		MinPlayCount: 3, Size: 30, RelaxFloor: 30 * day, MinUseful: 10,
	}
	got := BuildRediscover(tracks, opt)
	if len(got.TrackIDs) == 0 {
		t.Fatal("produced nothing. Nothing here is six months old, but 40 tracks are two months old and the user should still get a mix")
	}
	if !got.Relaxed {
		t.Error("widened the window without reporting it, so the description would claim a preference it did not meet")
	}
}

// Widening must never reach into this week's listening.
func TestRediscoverNeverOffersBackSomethingPlayedThisWeek(t *testing.T) {
	tracks := []Track{
		{ID: "yesterday", Starred: true, LastPlayed: ago(1 * day)},
		{ID: "last-week", Starred: true, LastPlayed: ago(5 * day)},
	}
	opt := RediscoverOptions{
		Now: now, MinAge: 180 * day, RecentGrace: 30 * day,
		MinPlayCount: 3, Size: 30, RelaxFloor: 30 * day, MinUseful: 10,
	}
	if got := BuildRediscover(tracks, opt); len(got.TrackIDs) != 0 {
		t.Errorf("selected %v, but nothing here is older than the floor", got.TrackIDs)
	}
}

// When the library genuinely has old favourites, the preference is honoured
// and the description must not claim it was relaxed.
func TestRediscoverDoesNotRelaxWhenItDoesNotHaveTo(t *testing.T) {
	var tracks []Track
	for i := 0; i < 20; i++ {
		tracks = append(tracks, Track{
			ID: "old" + string(rune('a'+i)), Starred: true, LastPlayed: ago(400 * day),
		})
	}
	got := BuildRediscover(tracks, RediscoverOptions{
		Now: now, MinAge: 180 * day, RecentGrace: 30 * day,
		MinPlayCount: 3, Size: 30, RelaxFloor: 30 * day, MinUseful: 10,
	})
	if got.Relaxed {
		t.Error("reported relaxed when 20 tracks met the preferred window")
	}
	if len(got.TrackIDs) != 20 {
		t.Errorf("selected %d, want 20", len(got.TrackIDs))
	}
}

// 0.9.3: a time-of-day mix changes from day to day within the slot's pool.
func TestTimeMixRotatesDailyInsideThePoolAndKeepsItsAnchors(t *testing.T) {
	var tracks []Track
	aff := SlotAffinity{}
	for i := 0; i < 120; i++ {
		id := "t" + strconv.Itoa(i)
		tracks = append(tracks, Track{ID: id, Artist: "a" + strconv.Itoa(i), PlayCount: 1})
		aff[id] = map[Slot]int{Morning: 200 - i}
	}
	opt := func(day int) TimeMixOptions {
		return TimeMixOptions{Slot: Morning, Affinity: aff, EventCount: 500,
			MinEventsForAffinity: 150, Size: 30, MaxPerArtist: 2, Day: day}
	}
	d1 := BuildTimeMix(tracks, opt(1)).TrackIDs
	d2 := BuildTimeMix(tracks, opt(2)).TrackIDs
	same := BuildTimeMix(tracks, opt(1)).TrackIDs
	if len(d1) != 30 || len(d2) != 30 {
		t.Fatalf("sizes %d %d, want 30", len(d1), len(d2))
	}
	if strings.Join(d1, ",") != strings.Join(same, ",") {
		t.Fatal("the same day must give the same mix")
	}
	if strings.Join(d1, ",") == strings.Join(d2, ",") {
		t.Fatal("two days must not give the same mix")
	}
	// 0.9.5: the five strongest stay in the mix every day, but NOT pinned to
	// the top. Presence is the guarantee; position is what rotates.
	for _, ids := range [][]string{d1, d2} {
		have := map[string]bool{}
		for _, id := range ids {
			have[id] = true
		}
		for i := 0; i < Anchors; i++ {
			want := "t" + strconv.Itoa(i)
			if !have[want] {
				t.Fatalf("anchor %s missing from %v", want, ids)
			}
		}
	}
	// Nothing from outside the top PoolFactor*Size ever appears.
	for _, id := range append(append([]string{}, d1...), d2...) {
		n, _ := strconv.Atoi(id[1:])
		if n >= 30*PoolFactor {
			t.Fatalf("%s is outside the pool", id)
		}
	}
	// Day 0 is the old behaviour: the plain top of the ranking.
	d0 := BuildTimeMix(tracks, opt(0)).TrackIDs
	if d0[29] != "t29" {
		t.Fatalf("day 0 must be the plain top 30, got last %s", d0[29])
	}
}

// 0.9.5 (Nenad, 2026-08-24: "ovaj morning mix je identican od prvog dana").
// 0.9.3 rotated 25 of the 30 tracks every day and it worked, but it emitted
// the five anchors first, in score order, so the VISIBLE head of the playlist
// was the same five tracks every day since the mix was created. Rotating the
// part nobody scrolls to is not a rotation the user can see.
func TestTimeMixHeadIsNotFrozenAcrossDays(t *testing.T) {
	var tracks []Track
	aff := SlotAffinity{}
	for i := 0; i < 120; i++ {
		id := "t" + strconv.Itoa(i)
		tracks = append(tracks, Track{ID: id, Artist: "a" + strconv.Itoa(i), PlayCount: 1})
		aff[id] = map[Slot]int{Morning: 200 - i}
	}
	opt := func(day int) TimeMixOptions {
		return TimeMixOptions{Slot: Morning, Affinity: aff, EventCount: 500,
			MinEventsForAffinity: 150, Size: 30, MaxPerArtist: 2, Day: day}
	}

	// Seven days in a row: the first row must not be the same track every day.
	firsts := map[string]int{}
	heads := map[string]int{}
	for day := 1; day <= 7; day++ {
		ids := BuildTimeMix(tracks, opt(day)).TrackIDs
		if len(ids) != 30 {
			t.Fatalf("day %d: %d tracks, want 30", day, len(ids))
		}
		for _, id := range ids {
			if id == "" {
				t.Fatalf("day %d: empty track id in %v", day, ids)
			}
		}
		firsts[ids[0]]++
		heads[strings.Join(ids[:Anchors], ",")]++
	}
	if len(firsts) < 2 {
		t.Fatalf("the first track was %v on all seven days, the head is frozen", firsts)
	}
	if len(heads) < 2 {
		t.Fatal("the first five rows were identical on all seven days, the head is frozen")
	}

	// And the anchors are still really in there, every day, which is the whole
	// reason they exist.
	for day := 1; day <= 7; day++ {
		have := map[string]bool{}
		for _, id := range BuildTimeMix(tracks, opt(day)).TrackIDs {
			have[id] = true
		}
		for i := 0; i < Anchors; i++ {
			if want := "t" + strconv.Itoa(i); !have[want] {
				t.Fatalf("day %d: anchor %s dropped", day, want)
			}
		}
	}
}

// The mix must never contain the same track twice, on any day.
func TestTimeMixHasNoDuplicatesAfterSpreadingTheAnchors(t *testing.T) {
	var tracks []Track
	aff := SlotAffinity{}
	for i := 0; i < 120; i++ {
		id := "t" + strconv.Itoa(i)
		tracks = append(tracks, Track{ID: id, Artist: "a" + strconv.Itoa(i), PlayCount: 1})
		aff[id] = map[Slot]int{Morning: 200 - i}
	}
	for day := 1; day <= 40; day++ {
		ids := BuildTimeMix(tracks, TimeMixOptions{Slot: Morning, Affinity: aff,
			EventCount: 500, MinEventsForAffinity: 150, Size: 30, MaxPerArtist: 2,
			Day: day}).TrackIDs
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("day %d: %s appears twice", day, id)
			}
			seen[id] = true
		}
		if len(ids) != 30 {
			t.Fatalf("day %d: %d tracks, want 30", day, len(ids))
		}
	}
}
