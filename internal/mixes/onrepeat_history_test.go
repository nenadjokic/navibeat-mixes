package mixes

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// moop250's report (r/navidrome, 2026-10): a song with a big lifetime count
// and one play this month outranked a song played over and over this month,
// because On Repeat scored by the LIFETIME play count. With the server's own
// play history the ranking is the number of plays inside the window.
func TestOnRepeatFromHistoryRanksByPlaysInsideTheWindow(t *testing.T) {
	tracks := []Track{
		{ID: "old-favourite", Artist: "A", PlayCount: 400, LastPlayed: ago(2 * day)},
		{ID: "this-month", Artist: "B", PlayCount: 9, LastPlayed: ago(1 * day)},
	}
	var plays []Play
	// old-favourite: two plays this month, plus a lifetime of plays before.
	plays = append(plays, Play{TrackID: "old-favourite", At: ago(2 * day)}, Play{TrackID: "old-favourite", At: ago(9 * day)})
	for i := 1; i <= 6; i++ {
		plays = append(plays, Play{TrackID: "this-month", At: ago(time.Duration(i) * day)})
	}
	got := BuildOnRepeatFromPlays(tracks, plays, now, 30*day, 10, 3)
	want := []string{"this-month", "old-favourite"}
	if !reflect.DeepEqual(got.TrackIDs, want) {
		t.Fatalf("On Repeat = %v, want %v: six plays this month must beat two, whatever the lifetime count", got.TrackIDs, want)
	}
	if got.Slot != OnRepeat {
		t.Fatalf("slot = %q, want %q", got.Slot, OnRepeat)
	}
}

// Same rule as the collector (internal/collector/histogram.go Accept): the
// same track inside max(60s, track duration) is ONE play. A stuck client that
// submits a track 46 times in a second must not put it on top.
func TestOnRepeatFromHistoryCollapsesDuplicateSubmissions(t *testing.T) {
	tracks := []Track{
		{ID: "stuck", Artist: "A", Duration: 4 * time.Minute},
		{ID: "real", Artist: "B", Duration: 3 * time.Minute},
	}
	var plays []Play
	base := ago(3 * day)
	// 46 submissions inside one second, then one more 2 minutes later (still
	// inside the 4 minute track), so all of it is a single play.
	for i := 0; i < 46; i++ {
		plays = append(plays, Play{TrackID: "stuck", At: base})
	}
	plays = append(plays, Play{TrackID: "stuck", At: base.Add(2 * time.Minute)})
	// A second, genuine play of "stuck" an hour later.
	plays = append(plays, Play{TrackID: "stuck", At: base.Add(time.Hour)})
	// "real": three genuine plays a day apart, delivered newest first, the
	// order a descending page would hand them over in.
	plays = append(plays,
		Play{TrackID: "real", At: ago(1 * day)},
		Play{TrackID: "real", At: ago(2 * day)},
		Play{TrackID: "real", At: ago(4 * day)},
	)

	tally := TallyPlays(plays, map[string]time.Duration{"stuck": 4 * time.Minute, "real": 3 * time.Minute})
	if tally["stuck"].Count != 2 {
		t.Fatalf("stuck counted %d plays, want 2", tally["stuck"].Count)
	}
	if tally["real"].Count != 3 {
		t.Fatalf("real counted %d plays, want 3", tally["real"].Count)
	}

	got := BuildOnRepeatFromPlays(tracks, plays, now, 30*day, 10, 3)
	want := []string{"real", "stuck"}
	if !reflect.DeepEqual(got.TrackIDs, want) {
		t.Fatalf("On Repeat = %v, want %v", got.TrackIDs, want)
	}
}

// A track with no known duration still gets the 60 second floor, exactly as
// the collector applies it.
func TestTallyPlaysUsesTheSixtySecondFloor(t *testing.T) {
	at := ago(day)
	plays := []Play{
		{TrackID: "t", At: at},
		{TrackID: "t", At: at.Add(59 * time.Second)},
		{TrackID: "t", At: at.Add(2 * time.Minute)},
	}
	if n := TallyPlays(plays, nil)["t"].Count; n != 2 {
		t.Fatalf("counted %d plays, want 2 (59s is a duplicate, 2 minutes is not)", n)
	}
}

// "At least two plays in the window" replaces the old PlayCount >= 2, plays
// outside the window do not count, and a play for a track that is not in the
// candidate pool (another library, a deleted file) is dropped.
func TestOnRepeatFromHistoryNeedsTwoPlaysInsideTheWindowAndInThePool(t *testing.T) {
	tracks := []Track{
		{ID: "once", Artist: "A"},
		{ID: "old", Artist: "B"},
		{ID: "twice", Artist: "C"},
	}
	plays := []Play{
		{TrackID: "once", At: ago(1 * day)},
		{TrackID: "old", At: ago(40 * day)},
		{TrackID: "old", At: ago(45 * day)},
		{TrackID: "old", At: ago(2 * day)},
		{TrackID: "twice", At: ago(3 * day)},
		{TrackID: "twice", At: ago(5 * day)},
		{TrackID: "not-in-pool", At: ago(1 * day)},
		{TrackID: "not-in-pool", At: ago(2 * day)},
		{TrackID: "not-in-pool", At: ago(3 * day)},
	}
	got := BuildOnRepeatFromPlays(tracks, plays, now, 30*day, 10, 3)
	if !reflect.DeepEqual(got.TrackIDs, []string{"twice"}) {
		t.Fatalf("On Repeat = %v, want [twice]", got.TrackIDs)
	}
}

// Equal counts break on the most recent play, then on id, so the same history
// always gives the same playlist.
func TestOnRepeatFromHistoryBreaksTiesOnTheMostRecentPlay(t *testing.T) {
	tracks := []Track{{ID: "a", Artist: "X"}, {ID: "b", Artist: "Y"}, {ID: "c", Artist: "Z"}}
	plays := []Play{
		{TrackID: "a", At: ago(10 * day)}, {TrackID: "a", At: ago(9 * day)},
		{TrackID: "b", At: ago(10 * day)}, {TrackID: "b", At: ago(1 * day)},
		{TrackID: "c", At: ago(10 * day)}, {TrackID: "c", At: ago(9 * day)},
	}
	got := BuildOnRepeatFromPlays(tracks, plays, now, 30*day, 10, 3)
	want := []string{"b", "a", "c"}
	if !reflect.DeepEqual(got.TrackIDs, want) {
		t.Fatalf("On Repeat = %v, want %v", got.TrackIDs, want)
	}
}

// The per-artist cap and the mix size still apply.
func TestOnRepeatFromHistoryKeepsTheArtistCapAndSize(t *testing.T) {
	var tracks []Track
	var plays []Play
	for _, id := range []string{"x1", "x2", "x3", "y1"} {
		artist := "X"
		if id == "y1" {
			artist = "Y"
		}
		tracks = append(tracks, Track{ID: id, Artist: artist})
		for i := 1; i <= 3; i++ {
			plays = append(plays, Play{TrackID: id, At: ago(time.Duration(i) * day)})
		}
	}
	got := BuildOnRepeatFromPlays(tracks, plays, now, 30*day, 2, 1)
	if len(got.TrackIDs) != 2 {
		t.Fatalf("got %v, want 2 tracks", got.TrackIDs)
	}
	if got.TrackIDs[0][0] == got.TrackIDs[1][0] {
		t.Fatalf("got %v, the cap of one per artist was ignored", got.TrackIDs)
	}
}

// THE OLD-SERVER PATH. When the history could not be read, On Repeat is
// exactly what it is today, and the description says why it is ranked the
// old way.
func TestPlanOnRepeatFallsBackToTodaysOutputWhenHistoryFails(t *testing.T) {
	tracks := []Track{
		{ID: "current", Artist: "A", PlayCount: 8, LastPlayed: ago(3 * day)},
		{ID: "old-hit", Artist: "B", PlayCount: 400, LastPlayed: ago(300 * day)},
		{ID: "busy", Artist: "C", PlayCount: 20, LastPlayed: ago(1 * day)},
	}
	today := BuildOnRepeat(tracks, now, 30*day, 10, 3)
	in := OnRepeatInput{Tracks: tracks, Now: now, Window: 30 * day, Size: 10, MaxPerArtist: 3}

	in.History = &PlayHistory{Err: errString("scrobbleRetriever: permission denied")}
	sel, desc := PlanOnRepeat(in)
	if !reflect.DeepEqual(sel, today) {
		t.Fatalf("fallback selection = %+v, want today's %+v", sel, today)
	}
	if desc != OnRepeatAllTimeDescription {
		t.Fatalf("fallback description = %q, want %q", desc, OnRepeatAllTimeDescription)
	}

	// A build that cannot read the history at all keeps today's output AND
	// today's description, word for word.
	in.History = nil
	sel, desc = PlanOnRepeat(in)
	if !reflect.DeepEqual(sel, today) {
		t.Fatalf("no-history selection = %+v, want today's %+v", sel, today)
	}
	if desc != "NaviBeat Mixes: what you have been playing over and over lately." {
		t.Fatalf("no-history description changed: %q", desc)
	}
}

func TestPlanOnRepeatUsesTheHistoryWhenItWasRead(t *testing.T) {
	tracks := []Track{
		{ID: "old-favourite", Artist: "A", PlayCount: 400, LastPlayed: ago(2 * day)},
		{ID: "this-month", Artist: "B", PlayCount: 9, LastPlayed: ago(1 * day)},
	}
	plays := []Play{
		{TrackID: "old-favourite", At: ago(2 * day)}, {TrackID: "old-favourite", At: ago(9 * day)},
		{TrackID: "this-month", At: ago(1 * day)}, {TrackID: "this-month", At: ago(2 * day)}, {TrackID: "this-month", At: ago(3 * day)},
	}
	in := OnRepeatInput{Tracks: tracks, Now: now, Window: 30 * day, Size: 10, MaxPerArtist: 3,
		History: &PlayHistory{Plays: plays}}
	sel, desc := PlanOnRepeat(in)
	if !reflect.DeepEqual(sel.TrackIDs, []string{"this-month", "old-favourite"}) {
		t.Fatalf("selection = %v", sel.TrackIDs)
	}
	if desc != "NaviBeat Mixes: the songs you played most in the last 30 days." {
		t.Fatalf("description = %q", desc)
	}
}

// Every description this file adds is held to the project's own rules.
func TestOnRepeatDescriptionsHaveNoEmDash(t *testing.T) {
	for _, d := range []string{OnRepeatAllTimeDescription, onRepeatHistoryDescription(30 * day)} {
		if strings.ContainsRune(d, '—') {
			t.Fatalf("em dash in %q", d)
		}
		if !strings.HasPrefix(d, "NaviBeat Mixes: ") {
			t.Fatalf("%q does not lead with the attribution", d)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
