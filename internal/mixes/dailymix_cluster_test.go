package mixes

import (
	"strconv"
	"strings"
	"testing"
)

// i-byrana (issue #14, 0.9.20): "Daily Mix 1 is just tracks from Paul
// Kalkbrenner", and Artist Radio 1 is Paul Kalkbrenner too. Daily Mix i is
// anchored on the artist of Artist Radio i+1 (main.go), and since 0.9.6 the
// anchor is exempt from MaxPerArtist and scores 20 above any neighbour. An
// anchor owning a full mix of tracks therefore took every slot, and the
// Daily Mix came out as the Artist Radio printed a second time.
func dailyMixLibrary() []Track {
	var tracks []Track
	for i := 0; i < 50; i++ {
		tracks = append(tracks, Track{ID: "kalk" + strconv.Itoa(i), Artist: "Paul Kalkbrenner", Genre: "Techno", PlayCount: 40})
	}
	for a := 0; a < 8; a++ {
		for i := 0; i < 6; i++ {
			tracks = append(tracks, Track{
				ID:     "near" + strconv.Itoa(a) + "-" + strconv.Itoa(i),
				Artist: "Techno Artist " + strconv.Itoa(a), Genre: "Techno", PlayCount: 5,
			})
		}
	}
	return tracks
}

func TestDailyMixIsAClusterNotTheArtistRadioAgain(t *testing.T) {
	tracks := dailyMixLibrary()

	daily := BuildDailyMix(tracks, []string{"Paul Kalkbrenner"}, 0, 30, 3).TrackIDs
	radio := BuildForArtist(tracks, "Paul Kalkbrenner", 30).TrackIDs

	if len(daily) != 30 {
		t.Fatalf("Daily Mix selected %d tracks, wanted 30", len(daily))
	}
	artists := map[string]bool{}
	anchor := 0
	for _, id := range daily {
		if strings.HasPrefix(id, "kalk") {
			anchor++
			artists["kalk"] = true
		} else {
			artists[strings.SplitN(id, "-", 2)[0]] = true
		}
	}
	if len(artists) < 4 {
		t.Errorf("Daily Mix holds %d artists, wanted a cluster of at least 4: %v", len(artists), daily)
	}
	if anchor > 30/2 {
		t.Errorf("the anchor took %d of 30 slots while neighbours were available, so the mix is still the Artist Radio", anchor)
	}
	if anchor == 0 || daily[0] != radio[0] {
		t.Errorf("Daily Mix opened on %q, wanted the anchor's strongest track %q first", daily[0], radio[0])
	}
	same := 0
	inRadio := map[string]bool{}
	for _, id := range radio {
		inRadio[id] = true
	}
	for _, id := range daily {
		if inRadio[id] {
			same++
		}
	}
	if same == len(daily) {
		t.Errorf("every Daily Mix track is also on the Artist Radio")
	}
}

// The anchor is not pushed to the bottom either: it is the artist the
// description names, so its tracks run through the whole mix.
func TestDailyMixSpreadsItsAnchorThroughTheMix(t *testing.T) {
	daily := BuildDailyMix(dailyMixLibrary(), []string{"Paul Kalkbrenner"}, 0, 30, 3).TrackIDs
	if len(daily) < 30 {
		t.Fatalf("Daily Mix selected %d tracks, wanted 30", len(daily))
	}
	half := 0
	for _, id := range daily[15:] {
		if strings.HasPrefix(id, "kalk") {
			half++
		}
	}
	if half == 0 {
		t.Errorf("the second half of the mix holds no anchor track: %v", daily)
	}
}
