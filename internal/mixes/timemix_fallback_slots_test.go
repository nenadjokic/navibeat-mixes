package mixes

import (
	"strconv"
	"strings"
	"testing"
)

// Issue #503489 (Bobby): in fallback mode, under the affinity threshold, the
// fallback score is play count plus a star bonus and has no time-of-day term,
// and every slot gets the same Day. Morning, Afternoon, Evening and Night came
// out as the same thirty tracks. "All playlists are pretty much the same songs
// slightly reshuffled."
func fallbackLibrary(n int) []Track {
	var tracks []Track
	for i := 0; i < n; i++ {
		tracks = append(tracks, Track{
			ID: "t" + strconv.Itoa(i), Artist: "a" + strconv.Itoa(i), PlayCount: 200 - i,
		})
	}
	return tracks
}

func fallbackOpt(slot Slot, day int) TimeMixOptions {
	return TimeMixOptions{Slot: slot, EventCount: 40, MinEventsForAffinity: 150,
		Size: 30, MaxPerArtist: 2, Day: day}
}

func setKey(ids []string) string {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return strings.Join(keys, ",")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestFallbackTimeMixesDifferBySlot(t *testing.T) {
	tracks := fallbackLibrary(120)
	const today = 20405
	lists := map[Slot][]string{}
	for _, slot := range TimeSlots {
		sel := BuildTimeMix(tracks, fallbackOpt(slot, today))
		if sel.Mode != ModeFallback {
			t.Fatalf("%s: mode %q, want fallback", slot, sel.Mode)
		}
		if len(sel.TrackIDs) != 30 {
			t.Fatalf("%s: %d tracks, want 30", slot, len(sel.TrackIDs))
		}
		lists[slot] = sel.TrackIDs
	}
	for i, a := range TimeSlots {
		for _, b := range TimeSlots[i+1:] {
			if strings.Join(lists[a], ",") == strings.Join(lists[b], ",") {
				t.Errorf("%s and %s are the same list in fallback", a, b)
			}
			if setKey(lists[a]) == setKey(lists[b]) {
				t.Errorf("%s and %s hold the same tracks, only reshuffled", a, b)
			}
		}
	}
}

// A refresh on the same day must give the same mix, or every run rewrites the
// playlist and it looks like it changed when nothing did.
func TestFallbackTimeMixIsStableWithinADay(t *testing.T) {
	tracks := fallbackLibrary(120)
	for _, slot := range TimeSlots {
		first := strings.Join(BuildTimeMix(tracks, fallbackOpt(slot, 20405)).TrackIDs, ",")
		for i := 0; i < 5; i++ {
			again := strings.Join(BuildTimeMix(tracks, fallbackOpt(slot, 20405)).TrackIDs, ",")
			if again != first {
				t.Fatalf("%s: run %d differed on the same day", slot, i)
			}
		}
		next := strings.Join(BuildTimeMix(tracks, fallbackOpt(slot, 20406)).TrackIDs, ",")
		if next == first {
			t.Errorf("%s: the next day gave the same mix", slot)
		}
	}
}

// A small history (fewer played tracks than the mix size) cannot give each
// slot different tracks, but the four playlists must still not be the same
// list in the same order.
func TestFallbackTimeMixesDifferBySlotOnASmallPool(t *testing.T) {
	tracks := fallbackLibrary(20)
	seen := map[string]Slot{}
	for _, slot := range TimeSlots {
		ids := BuildTimeMix(tracks, fallbackOpt(slot, 20405)).TrackIDs
		if len(ids) != 20 {
			t.Fatalf("%s: %d tracks, want all 20", slot, len(ids))
		}
		key := strings.Join(ids, ",")
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s are the same list on a small pool", other, slot)
		}
		seen[key] = slot
	}
}

// The fallback pool is still the listener's most played music: nothing from
// outside the top PoolFactor*Size reaches a slot.
func TestFallbackTimeMixStaysInsideThePool(t *testing.T) {
	tracks := fallbackLibrary(200)
	for _, slot := range TimeSlots {
		for _, id := range BuildTimeMix(tracks, fallbackOpt(slot, 20405)).TrackIDs {
			n, _ := strconv.Atoi(id[1:])
			if n >= 30*PoolFactor {
				t.Fatalf("%s: %s is outside the pool", slot, id)
			}
		}
	}
}
