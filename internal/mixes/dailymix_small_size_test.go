package mixes

import "testing"

// Code review 2026-10-24. config.Load accepts any positive mixSize (the
// manifest's minimum of 10 is a form hint, not a clamp), and pickDailyMix
// derived the anchor's share from the other groups by subtraction. At a size
// under four, with two related artists and other artists in the genre, the
// "room left for other artists" came out as 0, which takeIDsCapped reads as
// NO LIMIT, so every other track was picked and the anchor's share went
// negative: own[:n] with n < 0 panics, and a panic in a plugin call kills the
// whole generation run for every user, not just the one mix that would have
// been skipped for being under minMixSize anyway.
func TestDailyMixNeverPanicsOrOverfillsAtATinySize(t *testing.T) {
	for size := 1; size <= 12; size++ {
		got := BuildDailyMix(threeArtistLibrary(), []string{"Anchor"}, 0, size, 3).TrackIDs
		if len(got) > size {
			t.Errorf("size %d: Daily Mix selected %d tracks, more than the size asked for: %v", size, len(got), got)
		}
		if len(got) == 0 {
			t.Errorf("size %d: Daily Mix selected nothing although the anchor has 50 tracks", size)
		}
		if size >= 1 && got[0][0] != 'x' {
			t.Errorf("size %d: the mix opens on %q, wanted the anchor's strongest track", size, got[0])
		}
	}
}
