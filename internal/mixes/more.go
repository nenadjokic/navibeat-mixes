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
//
// IT RANKS ON THE DATE THE FILE WAS ADDED, which every Subsonic server sends on
// every song. The old code ranked on position in the candidate pool and said in
// a comment that the pool arrived "newest-first from the newest album list".
// It did not: the pool was starred tracks first, then most played, then most
// recently played, and no newest list was ever fetched. So the mix led with
// whatever the user had starred and called it new.
//
// Position is still the tie-break, for a server that sends no date at all.
func BuildNewMusic(tracks []Track, size, maxPerArtist int) Selection {
	pos := make(map[string]int, len(tracks))
	for i, t := range tracks {
		pos[t.ID] = i
	}
	ranked := make([]Track, len(tracks))
	copy(ranked, tracks)
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		// Sly777 (issue #1): "on new music playlist, there were many songs I
		// listened before." New here has always meant new to the LIBRARY, and
		// on a library you have been listening to for years that is not what
		// the name promises. Something already played is pushed behind
		// everything unheard rather than dropped, because a small or
		// thoroughly-played library would otherwise get an empty mix, which is
		// the failure the Rediscover builder above already learned to avoid.
		if (a.PlayCount > 0) != (b.PlayCount > 0) {
			return b.PlayCount > 0
		}
		// A missing date sorts behind every known one rather than ahead of
		// them: the zero time is "the server did not say", not 1970.
		if a.Added.IsZero() != b.Added.IsZero() {
			return b.Added.IsZero()
		}
		if !a.Added.Equal(b.Added) {
			return a.Added.After(b.Added)
		}
		return pos[a.ID] < pos[b.ID]
	})
	scored := make([]scoredTrack, 0, len(ranked))
	for i, t := range ranked {
		scored = append(scored, scoredTrack{track: t, score: len(ranked) - i})
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
	sortScored(scored)
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
	sortScored(scored)
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
	sortScored(scored)
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
		// Every genre the track carries, not just the legacy first one. A
		// library where Funk is almost always a SECOND genre used to rank
		// Funk near zero and never build it a radio.
		for _, g := range t.AllGenres() {
			plays[g] += t.PlayCount + 1 // presence counts a little, plays count more
		}
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
		if !t.HasGenre(genre) {
			continue
		}
		s := t.PlayCount + 1
		if t.Starred {
			s += 3
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}
	sortScored(scored)
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
	sortScored(scored)
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
	sortScored(scored)
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
		if !strings.EqualFold(t.Artist, anchor) {
			continue
		}
		for _, g := range t.AllGenres() {
			genres[strings.ToLower(g)] = true
		}
	}

	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		s := 0
		switch {
		case strings.EqualFold(t.Artist, anchor):
			s = 20 + t.PlayCount
		case t.sharesAnyGenre(genres):
			s = t.PlayCount + 1
		default:
			continue
		}
		if t.Starred {
			s += 3
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}
	sortScored(scored)
	return Selection{
		Slot:     DailyMix,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// WeekIndex is a stable rotation counter that advances once per week.
//
// It is derived from the date and nothing else, so every device and every run
// inside the same week computes the same number. That is what lets a mix
// change its contents weekly while keeping a fixed name: the identity is the
// number in the title, the content is a window into a deeper pool, and the
// window only moves when the week does.
func WeekIndex(now time.Time) int {
	// Days since the Unix epoch, bucketed into weeks.
	//
	// The +3 matters and a test caught its absence: epoch day 0 is a THURSDAY,
	// so dividing raw epoch days by 7 produces buckets that run Thursday to
	// Wednesday. A listener would then see their mixes change midweek, which
	// reads as the playlist being rewritten under them. Shifting by 3 aligns
	// the boundary to Monday, so a week is a week as a person experiences it.
	//
	// Deliberately not ISO week numbers: those reset every January and would
	// send the rotation lurching backwards each new year.
	return (int(now.Unix()/(60*60*24)) + 3) / 7
}

// Rotate returns a window of `size` items starting at an offset derived from
// the week, wrapping around the pool.
//
// A deeper pool than the window is the whole point: with 20 artists and a
// window of 5, a listener meets a different five every week and the whole
// library comes round eventually, instead of seeing the same top five forever.
func Rotate[T any](pool []T, week, size int) []T {
	if len(pool) == 0 || size <= 0 {
		return nil
	}
	if size > len(pool) {
		size = len(pool)
	}
	start := 0
	if len(pool) > 0 {
		start = (week * size) % len(pool)
	}
	out := make([]T, 0, size)
	for i := 0; i < size; i++ {
		out = append(out, pool[(start+i)%len(pool)])
	}
	return out
}
