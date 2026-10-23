package mixes

import (
	"strconv"
	"strings"
	"testing"
)

// vbrzezina (issue #14, after 0.9.21): "right now the anchor artist tracks are
// all sorted in top". The anchor's tracks are placed by spreadAnchors, one per
// band. With integer spacing, step = n / anchors falls to 1 as soon as the
// anchor holds more than half the mix (a library with few genre neighbours), and
// every anchor track then lands in the first slots in one block.
func dailyMixLibraryWithNeighbours(neighbourArtists, perArtist int) []Track {
	var tracks []Track
	for i := 0; i < 50; i++ {
		tracks = append(tracks, Track{ID: "kalk" + strconv.Itoa(i), Artist: "Paul Kalkbrenner", Genre: "Techno", PlayCount: 40})
	}
	for a := 0; a < neighbourArtists; a++ {
		for i := 0; i < perArtist; i++ {
			tracks = append(tracks, Track{
				ID:     "near" + strconv.Itoa(a) + "-" + strconv.Itoa(i),
				Artist: "Techno Artist " + strconv.Itoa(a), Genre: "Techno", PlayCount: 5,
			})
		}
	}
	return tracks
}

func anchorPattern(ids []string) (string, int, int) {
	var b strings.Builder
	longest, run, anchors := 0, 0, 0
	for _, id := range ids {
		if strings.HasPrefix(id, "kalk") {
			b.WriteByte('A')
			anchors++
			run++
			if run > longest {
				longest = run
			}
		} else {
			b.WriteByte('n')
			run = 0
		}
	}
	return b.String(), longest, anchors
}

func TestDailyMixAnchorTracksAreSpreadThroughTheMix(t *testing.T) {
	cases := []struct {
		name             string
		neighbourArtists int
		perArtist        int
	}{
		{"many neighbours", 8, 6},
		{"few neighbours", 3, 6},
		{"one neighbour", 1, 6},
	}
	for _, c := range cases {
		ids := BuildDailyMix(dailyMixLibraryWithNeighbours(c.neighbourArtists, c.perArtist),
			[]string{"Paul Kalkbrenner"}, 0, 30, 3).TrackIDs
		pattern, longest, anchors := anchorPattern(ids)
		near := len(ids) - anchors
		t.Logf("%-16s %s (anchor %d, others %d, longest anchor run %d)", c.name, pattern, anchors, near, longest)
		if len(ids) == 0 || !strings.HasPrefix(ids[0], "kalk") {
			t.Errorf("%s: the mix does not open on the anchor: %s", c.name, pattern)
		}
		if near == 0 {
			continue
		}
		// Spread evenly, no anchor run is longer than the anchors-per-gap share
		// rounded up, plus one for the opening track.
		limit := (anchors+near)/(near+1) + 1
		if longest > limit {
			t.Errorf("%s: anchor tracks bunch up, longest run %d > %d: %s", c.name, longest, limit, pattern)
		}
	}
}
