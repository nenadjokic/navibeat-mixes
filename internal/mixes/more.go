package mixes

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// The mixes in this file exist to make a separate playlist plugin unnecessary.
//
// ONE RULE SHAPES ALL OF THEM, and it is the difference between this and the
// plugin they replace: a playlist name NEVER contains variable data. Not an
// artist, not a genre, not a rank. Naming a playlist "Artist Radio 1: LINKIN
// PARK" means that the day your listening shifts, the name changes, a brand
// new playlist appears, and the old one is orphaned. That is why servers
// running such a plugin end up needing a cleanup script on a cron. Here the
// name is "Artist Radio 1" and the artist goes in the DESCRIPTION, which
// changes freely because nothing keys on it.

// Extra slots. Numbered ones are stable identities: "Genre Radio 2" is always
// the second strongest genre, whatever that turns out to be this week.
const (
	NewMusic     Slot = "newmusic"
	LovedSongs   Slot = "loved"
	OnRepeat     Slot = "onrepeat"
	Essentials   Slot = "essentials"
	Discovery    Slot = "discovery"
	GenreRadio   Slot = "genreradio"
	ArtistRadio  Slot = "artistradio"
	DailyMix     Slot = "dailymix"
	DecadeMix    Slot = "decade"
)

// NumberedSlot builds the stable slot key for a numbered mix, for example
// "genreradio-2".
func NumberedSlot(base Slot, n int) string {
	return string(base) + "-" + strconv.Itoa(n)
}

// BuildNewMusic is the newest additions to the library. It needs no history at
// all, which makes it one of the few mixes that is genuinely good on install
// day, so it is worth having even on a server with no listening data.
func BuildNewMusic(tracks []Track, size, maxPerArtist int) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for i, t := range tracks {
		// Candidates arrive newest-first from the newest album list, so
		// position is the recency signal. Using a date here would need one
		// that the API does not expose per track.
		scored = append(scored, scoredTrack{track: t, score: len(tracks) - i})
	}
	return Selection{
		Slot:     NewMusic,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// BuildLovedSongs is everything starred, oldest favourite first so the mix
// does not simply repeat what was starred this week.
func BuildLovedSongs(tracks []Track, size, maxPerArtist int) Selection {
	var loved []Track
	for _, t := range tracks {
		if t.Starred {
			loved = append(loved, t)
		}
	}
	sortOldestFirst(loved)
	scored := make([]scoredTrack, 0, len(loved))
	for i, t := range loved {
		scored = append(scored, scoredTrack{track: t, score: len(loved) - i})
	}
	return Selection{
		Slot:     LovedSongs,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// BuildOnRepeat is what the user is playing a lot RIGHT NOW: high play count
// and touched recently. The recency requirement is what separates it from
// Essentials, which is an all-time list.
func BuildOnRepeat(tracks []Track, now time.Time, window time.Duration, size, maxPerArtist int) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		if t.LastPlayed.IsZero() || now.Sub(t.LastPlayed) > window || t.PlayCount < 2 {
			continue
		}
		scored = append(scored, scoredTrack{track: t, score: t.PlayCount})
	}
	return Selection{
		Slot:     OnRepeat,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// BuildEssentials is the all-time most played, the closest thing a library has
// to a personal chart.
func BuildEssentials(tracks []Track, size, maxPerArtist int) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		if t.PlayCount <= 0 {
			continue
		}
		s := t.PlayCount
		if t.Starred {
			s += 5
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}
	return Selection{
		Slot:     Essentials,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// BuildDiscovery surfaces music the user owns and has barely touched.
//
// Deliberately NOT "never played": a library is full of tracks that arrived
// with an album and were never anyone's choice. Requiring at least one play,
// and a long gap since, finds things that were once worth playing and then
// forgotten, which is a much better recommendation than a random unplayed file.
func BuildDiscovery(tracks []Track, now time.Time, minGap time.Duration, size, maxPerArtist int) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		if t.PlayCount < 1 || t.PlayCount > 3 || t.Starred {
			continue
		}
		if t.LastPlayed.IsZero() || now.Sub(t.LastPlayed) < minGap {
			continue
		}
		// Fewer plays and longer forgotten scores higher.
		days := int(now.Sub(t.LastPlayed).Hours() / 24)
		scored = append(scored, scoredTrack{track: t, score: days/7 + (4 - t.PlayCount)})
	}
	return Selection{
		Slot:     Discovery,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// TopGenres ranks genres by how much the user actually plays them, not by how
// many files carry the tag. A genre with 3000 untouched tracks is a shelf, not
// a taste.
func TopGenres(tracks []Track, n int) []string {
	plays := map[string]int{}
	for _, t := range tracks {
		g := strings.TrimSpace(t.Genre)
		if g == "" {
			continue
		}
		plays[g] += t.PlayCount + 1 // presence counts a little, plays count more
	}
	type gp struct {
		name string
		n    int
	}
	all := make([]gp, 0, len(plays))
	for g, c := range plays {
		all = append(all, gp{g, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].name < all[j].name
	})
	out := make([]string, 0, n)
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].name)
	}
	return out
}

// TopArtists ranks artists the same way.
func TopArtists(tracks []Track, n int) []string {
	plays := map[string]int{}
	for _, t := range tracks {
		a := strings.TrimSpace(t.Artist)
		if a == "" {
			continue
		}
		plays[a] += t.PlayCount
	}
	type ap struct {
		name string
		n    int
	}
	all := make([]ap, 0, len(plays))
	for a, c := range plays {
		if c <= 0 {
			continue
		}
		all = append(all, ap{a, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].name < all[j].name
	})
	out := make([]string, 0, n)
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].name)
	}
	return out
}

// BuildForGenre is one genre radio.
func BuildForGenre(tracks []Track, genre string, size, maxPerArtist int) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		if !strings.EqualFold(strings.TrimSpace(t.Genre), genre) {
			continue
		}
		s := t.PlayCount + 1
		if t.Starred {
			s += 3
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}
	return Selection{
		Slot:     GenreRadio,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// BuildForArtist is one artist radio: that artist plus nothing else. Keeping it
// to the artist is honest about what it is; claiming "and similar artists"
// would need a similarity source this plugin does not have.
func BuildForArtist(tracks []Track, artist string, size int) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		if !strings.EqualFold(strings.TrimSpace(t.Artist), artist) {
			continue
		}
		s := t.PlayCount + 1
		if t.Starred {
			s += 3
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}
	// No per-artist cap here: the whole point is one artist.
	return Selection{
		Slot:     ArtistRadio,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, 0),
	}
}

// Decade returns the decade a year belongs to, for example 1994 -> 1990.
func Decade(year int) int { return (year / 10) * 10 }

// TopDecades ranks decades by plays.
func TopDecades(tracks []Track, n int) []int {
	plays := map[int]int{}
	for _, t := range tracks {
		if t.Year < 1900 {
			continue
		}
		plays[Decade(t.Year)] += t.PlayCount + 1
	}
	type dp struct{ d, n int }
	all := make([]dp, 0, len(plays))
	for d, c := range plays {
		all = append(all, dp{d, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].d > all[j].d
	})
	out := make([]int, 0, n)
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].d)
	}
	return out
}

// BuildForDecade is one decade mix.
func BuildForDecade(tracks []Track, decade, size, maxPerArtist int) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		if t.Year < 1900 || Decade(t.Year) != decade {
			continue
		}
		s := t.PlayCount + 1
		if t.Starred {
			s += 3
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}
	return Selection{
		Slot:     DecadeMix,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// BuildDailyMix is one of several rotating mixes, each anchored on a different
// artist so the set covers a spread of the user's taste rather than three
// variations of the same thing.
//
// `index` selects which anchor, so "Daily Mix 2" is a stable identity even
// though its contents change.
func BuildDailyMix(tracks []Track, anchors []string, index, size, maxPerArtist int) Selection {
	if index < 0 || index >= len(anchors) {
		return Selection{Slot: DailyMix, Mode: ModeFallback}
	}
	anchor := anchors[index]

	// Genres this anchor lives in, so the mix can widen beyond one artist
	// without wandering off into unrelated music.
	genres := map[string]bool{}
	for _, t := range tracks {
		if strings.EqualFold(t.Artist, anchor) && t.Genre != "" {
			genres[strings.ToLower(t.Genre)] = true
		}
	}

	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		s := 0
		switch {
		case strings.EqualFold(t.Artist, anchor):
			s = 20 + t.PlayCount
		case t.Genre != "" && genres[strings.ToLower(t.Genre)]:
			s = t.PlayCount + 1
		default:
			continue
		}
		if t.Starred {
			s += 3
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}
	return Selection{
		Slot:     DailyMix,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}
