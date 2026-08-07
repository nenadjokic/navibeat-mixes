package mixes

import "testing"

// Sly777, issue #1, 2026-08-02: "genre radio 3 had just Outkast but it called
// Funk on description. There was no other musician or song. I have many other
// funk songs."
//
// The cause was not the selection at all. Subsonic's legacy `genre` field
// carries ONE value, so a track tagged "Hip-Hop; Funk" reports "Hip-Hop" and
// nothing else, and the Funk radio could only ever see tracks whose FIRST
// genre was literally Funk. On his library that was one artist.
//
// This test fails on the old code and passes on the new one.
func TestGenreRadioSeesSecondaryGenres(t *testing.T) {
	tracks := []Track{
		// The one artist whose PRIMARY genre is Funk. Before the fix this was
		// the whole radio.
		{ID: "1", Artist: "Outkast", Genre: "Funk", Genres: []string{"Funk"}},
		// Everything the user meant by "many other funk songs": Funk is there,
		// just not first.
		{ID: "2", Artist: "Parliament", Genre: "Hip-Hop", Genres: []string{"Hip-Hop", "Funk"}},
		{ID: "3", Artist: "Chic", Genre: "Disco", Genres: []string{"Disco", "Funk"}},
		{ID: "4", Artist: "Sly and the Family Stone", Genre: "Soul", Genres: []string{"Soul", "Funk"}},
		// Genuinely not funk, must stay out.
		{ID: "5", Artist: "Metallica", Genre: "Metal", Genres: []string{"Metal"}},
	}

	got := BuildForGenre(tracks, "Funk", 10, 0)
	if len(got.TrackIDs) != 4 {
		t.Fatalf("expected the four funk tracks, got %d: %v", len(got.TrackIDs), got.TrackIDs)
	}
	for _, id := range got.TrackIDs {
		if id == "5" {
			t.Fatalf("Metallica is not funk: %v", got.TrackIDs)
		}
	}
}

// A server that never implements the OpenSubsonic array must behave exactly as
// it did before. This is the half that protects everybody who was not affected.
func TestLegacySingleGenreStillWorks(t *testing.T) {
	tracks := []Track{
		{ID: "1", Artist: "A", Genre: "Funk"},
		{ID: "2", Artist: "B", Genre: "Metal"},
	}
	got := BuildForGenre(tracks, "funk", 10, 0)
	if len(got.TrackIDs) != 1 || got.TrackIDs[0] != "1" {
		t.Fatalf("legacy path changed behaviour: %v", got.TrackIDs)
	}
}

// A genre that is almost always secondary used to rank near zero and never got
// a radio built for it at all, which is the other half of the same report.
func TestTopGenresCountsEveryGenreOnATrack(t *testing.T) {
	tracks := []Track{
		{ID: "1", Genre: "Rock", Genres: []string{"Rock", "Funk"}, PlayCount: 5},
		{ID: "2", Genre: "Rock", Genres: []string{"Rock", "Funk"}, PlayCount: 5},
		{ID: "3", Genre: "Pop", Genres: []string{"Pop"}, PlayCount: 1},
	}
	got := TopGenres(tracks, 3)
	var sawFunk bool
	for _, g := range got {
		if g == "Funk" {
			sawFunk = true
		}
	}
	if !sawFunk {
		t.Fatalf("Funk never ranked despite being on the two most played tracks: %v", got)
	}
}

// FilterGenres exists to drop genres that carry no information. On a
// multi-genre library it used to blank the track's genre entirely when its ONE
// value was denied, throwing away the useful values with the useless one.
func TestFilterGenresDropsOnlyTheUselessGenre(t *testing.T) {
	tracks := []Track{
		{ID: "1", Genre: "Music", Genres: []string{"Music", "Funk"}},
		{ID: "2", Genre: "Music", Genres: []string{"Music", "Jazz"}},
	}
	out := FilterGenres(tracks, []string{"Music"}, 1.0)
	for _, tr := range out {
		gs := tr.AllGenres()
		if len(gs) != 1 {
			t.Fatalf("track %s should keep exactly its useful genre, got %v", tr.ID, gs)
		}
		if gs[0] == "Music" {
			t.Fatalf("the denied genre survived on track %s: %v", tr.ID, gs)
		}
	}
}

// The threshold rule must still fire, and it counts per genre rather than per
// track now, which is the correct denominator when a track has several.
func TestFilterGenresStillDropsAnOverwhelmingGenre(t *testing.T) {
	var tracks []Track
	for i := 0; i < 10; i++ {
		tracks = append(tracks, Track{ID: string(rune('a' + i)), Genre: "Everything", Genres: []string{"Everything"}})
	}
	tracks = append(tracks, Track{ID: "z", Genre: "Funk", Genres: []string{"Funk"}})
	out := FilterGenres(tracks, nil, 0.5)
	for _, tr := range out {
		if tr.HasGenre("Everything") {
			t.Fatalf("a genre on almost the whole library should have been dropped: %v", tr.AllGenres())
		}
	}
	if !out[len(out)-1].HasGenre("Funk") {
		t.Fatalf("Funk should have survived")
	}
}

// Sly777, issue #1: "on new music playlist, there were many songs I listened
// before. Maybe it would be better if new music playlist could check if that
// song listened less than X."
//
// New meant new to the LIBRARY, which on a library you have owned for years is
// not what the name promises. Unheard tracks now come first, and played ones
// still follow rather than being dropped, so a thoroughly played library does
// not end up with an empty mix.
func TestNewMusicPutsUnheardTracksFirst(t *testing.T) {
	// Newest first, which is the order candidates arrive in.
	tracks := []Track{
		{ID: "played-newest", Artist: "A", PlayCount: 9},
		{ID: "unheard-1", Artist: "B"},
		{ID: "played-older", Artist: "C", PlayCount: 3},
		{ID: "unheard-2", Artist: "D"},
	}
	got := BuildNewMusic(tracks, 4, 0)
	if len(got.TrackIDs) != 4 {
		t.Fatalf("nothing should be dropped, got %v", got.TrackIDs)
	}
	if got.TrackIDs[0] != "unheard-1" || got.TrackIDs[1] != "unheard-2" {
		t.Fatalf("unheard tracks should lead, and keep their recency order: %v", got.TrackIDs)
	}
	if got.TrackIDs[2] != "played-newest" || got.TrackIDs[3] != "played-older" {
		t.Fatalf("played tracks should follow, newest of them first: %v", got.TrackIDs)
	}
}
