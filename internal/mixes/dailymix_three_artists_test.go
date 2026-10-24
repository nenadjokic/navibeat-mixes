package mixes

import (
	"strconv"
	"strings"
	"testing"
)

// vbrzezina (issue #14, after 0.9.22): a Daily Mix should be several artists,
// not one artist and filler. Nenad chose the shape (D503061-DAILY A, 2026-10-23):
// three related artists with 6 tracks each, plus 12 tracks from other artists in
// the same genres, at a mix size of 30.
func threeArtistLibrary() []Track {
	var tracks []Track
	add := func(prefix, artist, genre string, n, plays int) {
		for i := 0; i < n; i++ {
			tracks = append(tracks, Track{ID: prefix + strconv.Itoa(i), Artist: artist, Genre: genre, PlayCount: plays})
		}
	}
	add("x", "Anchor", "Techno", 50, 40)
	add("y", "Second", "Techno", 20, 30)
	add("z", "Third", "Techno", 20, 20)
	add("w", "Fourth", "Techno", 20, 10)
	for a := 0; a < 6; a++ {
		add("n"+strconv.Itoa(a)+"-", "Neighbour "+strconv.Itoa(a), "Techno", 6, 5)
	}
	return tracks
}

func artistOf(id string) string {
	switch id[0] {
	case 'x', 'y', 'z', 'w':
		return id[:1]
	}
	return strings.SplitN(id, "-", 2)[0]
}

func TestDailyMixIsThreeArtistsPlusNeighbours(t *testing.T) {
	got := BuildDailyMix(threeArtistLibrary(), []string{"Anchor"}, 0, 30, 3).TrackIDs
	if len(got) != 30 {
		t.Fatalf("Daily Mix selected %d tracks, wanted 30: %v", len(got), got)
	}
	count := map[string]int{}
	for _, id := range got {
		count[artistOf(id)]++
	}
	for _, a := range []string{"x", "y", "z"} {
		if count[a] != 6 {
			t.Errorf("featured artist %s has %d tracks, wanted 6: %v", a, count[a], count)
		}
	}
	others := 0
	for a, n := range count {
		if a == "x" || a == "y" || a == "z" {
			continue
		}
		others += n
		if n > 3 {
			t.Errorf("artist %s has %d tracks, the cap for everyone outside the three is 3", a, n)
		}
	}
	if others != 12 {
		t.Errorf("%d tracks from other artists, wanted 12: %v", others, count)
	}
	if got[0][0] != 'x' {
		t.Errorf("the mix opens on %q, wanted the anchor's strongest track", got[0])
	}
}

// The three featured artists take turns through the mix instead of coming in
// blocks, so it never plays two tracks in a row by one of them.
func TestDailyMixInterleavesItsThreeArtists(t *testing.T) {
	got := BuildDailyMix(threeArtistLibrary(), []string{"Anchor"}, 0, 30, 3).TrackIDs
	var b strings.Builder
	for i, id := range got {
		a := artistOf(id)
		switch a {
		case "x", "y", "z":
			b.WriteString(strings.ToUpper(a))
		default:
			b.WriteByte('n')
		}
		if i > 0 && a == artistOf(got[i-1]) && (a == "x" || a == "y" || a == "z") {
			t.Errorf("two tracks in a row by featured artist %s at %d", a, i)
		}
	}
	t.Logf("pattern %s", b.String())
	firstHalf := map[byte]bool{}
	for _, c := range []byte(b.String()[:15]) {
		firstHalf[c] = true
	}
	for _, c := range []byte("XYZn") {
		if !firstHalf[c] {
			t.Errorf("the first half of the mix holds no %q: %s", c, b.String())
		}
	}
}

// Related means sharing the anchor's genres. An artist that shares two of them
// is closer than one that shares one, however much more the second is played.
func TestDailyMixPicksRelatedArtistsByGenresSharedFirst(t *testing.T) {
	var tracks []Track
	for i := 0; i < 20; i++ {
		tracks = append(tracks, Track{ID: "x" + strconv.Itoa(i), Artist: "Anchor", Genre: "Techno", Genres: []string{"Techno", "House"}, PlayCount: 40})
		tracks = append(tracks, Track{ID: "p" + strconv.Itoa(i), Artist: "Close", Genre: "Techno", Genres: []string{"Techno", "House"}, PlayCount: 1})
		tracks = append(tracks, Track{ID: "q" + strconv.Itoa(i), Artist: "Loud", Genre: "Techno", PlayCount: 100})
		tracks = append(tracks, Track{ID: "r" + strconv.Itoa(i), Artist: "Middle", Genre: "House", PlayCount: 50})
	}
	got := BuildDailyMix(tracks, []string{"Anchor"}, 0, 30, 3).TrackIDs
	n := map[byte]int{}
	for _, id := range got {
		n[id[0]]++
	}
	if n['p'] != 6 {
		t.Errorf("the artist sharing both genres has %d tracks, wanted 6 as a featured artist: %v", n['p'], n)
	}
	if n['q'] != 6 {
		t.Errorf("the most played one-genre artist has %d tracks, wanted 6 as the third featured artist: %v", n['q'], n)
	}
	if n['r'] > 3 {
		t.Errorf("the fourth artist has %d tracks, wanted at most the cap of 3: %v", n['r'], n)
	}
}

// The description names the artists the listener will hear, in the spelling of
// their tags, anchor first.
func TestDailyMixReportsItsFeaturedArtists(t *testing.T) {
	got := BuildDailyMix(threeArtistLibrary(), []string{"Anchor"}, 0, 30, 3).Featured
	want := []string{"Anchor", "Second", "Third"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Featured = %v, want %v", got, want)
	}
}

// A related artist that has no track in the mix is not named.
func TestDailyMixDoesNotNameAnArtistItDoesNotPlay(t *testing.T) {
	// At a mix size of 3 the share is 1 track each, then the budget is spent
	// before the second related artist is reached.
	sel := BuildDailyMix(threeArtistLibrary(), []string{"Anchor"}, 0, 2, 3)
	if len(sel.TrackIDs) != 2 {
		t.Fatalf("selected %d tracks, wanted 2: %v", len(sel.TrackIDs), sel.TrackIDs)
	}
	for _, name := range sel.Featured {
		played := false
		for _, id := range sel.TrackIDs {
			if strings.EqualFold(name, map[string]string{"x": "Anchor", "y": "Second", "z": "Third"}[id[:1]]) {
				played = true
			}
		}
		if !played {
			t.Errorf("%q is named but has no track in %v", name, sel.TrackIDs)
		}
	}
}

func TestFeaturedArtistsUseTheTagSpelling(t *testing.T) {
	tracks := []Track{
		{ID: "a", Artist: " Muse "}, {ID: "b", Artist: "SICK PUPPIES"}, {ID: "c", Artist: "sick puppies"},
	}
	got := featuredArtists(tracks, "Muse", []string{"sick puppies"}, []string{"a", "b"})
	if strings.Join(got, "|") != "Muse|SICK PUPPIES" {
		t.Errorf("featuredArtists = %v", got)
	}
}
