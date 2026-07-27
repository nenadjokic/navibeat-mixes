package mixes

import "testing"

func TestWrappedRanksByObservedPlaysNotLibraryPlayCount(t *testing.T) {
	tracks := []Track{
		// A huge all-time library count, but the plugin never saw it played.
		{ID: "old-favourite", PlayCount: 500},
		{ID: "this-year", PlayCount: 2},
	}
	got := BuildWrapped(tracks, WrappedOptions{
		Plays: map[string]int{"this-year": 40},
		Size:  10,
	})
	if len(got.TrackIDs) != 1 || got.TrackIDs[0] != "this-year" {
		t.Fatalf("selected %v, want only this-year: a recap covers what was OBSERVED, not all-time counts", got.TrackIDs)
	}
}

func TestWrappedCapsOneArtistOwningTheYear(t *testing.T) {
	var tracks []Track
	plays := map[string]int{}
	for i := 0; i < 10; i++ {
		id := "hog" + string(rune('a'+i))
		tracks = append(tracks, Track{ID: id, Artist: "Prolific"})
		plays[id] = 100 - i
	}
	tracks = append(tracks, Track{ID: "other", Artist: "Someone Else"})
	plays["other"] = 1

	got := BuildWrapped(tracks, WrappedOptions{Plays: plays, Size: 10, MaxPerArtist: 3})
	hogs := 0
	for _, id := range got.TrackIDs {
		if len(id) > 3 && id[:3] == "hog" {
			hogs++
		}
	}
	if hogs > 3 {
		t.Errorf("one artist took %d of the recap, cap was 3", hogs)
	}
}

func TestWrappedIsEmptyBeforeAnythingWasObserved(t *testing.T) {
	tracks := []Track{{ID: "a", PlayCount: 99}}
	got := BuildWrapped(tracks, WrappedOptions{Plays: nil, Size: 10})
	if len(got.TrackIDs) != 0 {
		t.Errorf("selected %v with no observed plays, want nothing", got.TrackIDs)
	}
}

// Each year gets its own playlist. Overwriting last year's recap would delete
// something a user may well have kept on purpose.
func TestWrappedNamesAreDistinctPerYear(t *testing.T) {
	if WrappedName(2026) == WrappedName(2027) {
		t.Error("two years share a playlist name")
	}
	if WrappedSlot(2026) == WrappedSlot(2027) {
		t.Error("two years share a slot key, a client could not tell them apart")
	}
}
