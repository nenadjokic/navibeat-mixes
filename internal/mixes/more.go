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
	NewMusic    Slot = "newmusic"
	LovedSongs  Slot = "loved"
	OnRepeat    Slot = "onrepeat"
	Essentials  Slot = "essentials"
	Discovery   Slot = "discovery"
	GenreRadio  Slot = "genreradio"
	ArtistRadio Slot = "artistradio"
	DailyMix    Slot = "dailymix"
	DecadeMix   Slot = "decade"
)

// NumberedSlot builds the stable slot key for a numbered mix, for example
// "genreradio-2".
func NumberedSlot(base Slot, n int) string {
	return string(base) + "-" + strconv.Itoa(n)
}

// NewMusicOrder is what "new" means to the New Music mix.
type NewMusicOrder string

const (
	// NewMusicByAdded ranks on the date the file reached the server. The
	// default, and the only order there was before 0.9.8.
	NewMusicByAdded NewMusicOrder = "added"
	// NewMusicByReleased ranks on the release date in the tags, newest first,
	// and falls back to the added date only for tracks with no date at all.
	NewMusicByReleased NewMusicOrder = "released"
)

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
//
// Kept with its original signature and its original order so every caller,
// every test and every pinned playlist behaves exactly as before 0.9.8; the
// release-date order lives in BuildNewMusicBy.
func BuildNewMusic(tracks []Track, size, maxPerArtist int) Selection {
	return BuildNewMusicBy(tracks, NewMusicByAdded, size, maxPerArtist)
}

// BuildNewMusicBy is BuildNewMusic with a choice of what "new" means.
//
// Steven O'Neil asked whether New Music was the release date or the date the
// file was added, because on a library that is still being ripped the two
// disagree completely: a 1984 record added yesterday is the newest thing on
// the server and the oldest thing in the mix. Both readings are legitimate,
// so it is a setting, and the default is the one every existing install has.
//
// The released order, in this sequence:
//
//  1. Played tracks behind unplayed ones (unchanged, Sly777's rule above).
//  2. A known release date before an unknown one.
//  3. Newer release date first.
//  4. Equal release date, then newer ADDED date first, so a library with
//     year-only tags still moves with what arrived this week.
//  5. Position in the pool.
//
// A year-only date is 1 January of that year, so within one year an album
// tagged with a full date outranks one tagged with only the year. That is a
// property of the tags, and it is documented rather than hidden. A library
// with no dates at all lands entirely in the unknown group and comes out in
// exactly the added order, so this mode can never do worse than the default.
func BuildNewMusicBy(tracks []Track, order NewMusicOrder, size, maxPerArtist int) Selection {
	pos := make(map[string]int, len(tracks))
	for i, t := range tracks {
		pos[t.ID] = i
	}
	ranked := make([]Track, len(tracks))
	copy(ranked, tracks)
	byReleased := order == NewMusicByReleased
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
		if byReleased {
			if a.Released.IsZero() != b.Released.IsZero() {
				return b.Released.IsZero()
			}
			if !a.Released.Equal(b.Released) {
				return a.Released.After(b.Released)
			}
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
	return TopGenresOwning(tracks, n, 0, 0)
}

// TopGenresOwning is TopGenres with a floor on how many tracks the genre must
// have IN THE CANDIDATE POOL to be eligible at all.
//
// The same defect TopArtistsOwning exists for, in the same shape: ranking is by
// plays, so a genre carried by ONE heavily played track walks into the top 12,
// gets numbered, produces a one track Genre Radio, and is then dropped for
// being under minMixSize. The number goes with it and Genre Radio starts at 2.
// Fixing the artists and leaving this alone would have shipped the reporter
// the same bug on a different shelf.
//
// ⛔ THE FILTER RUNS BEFORE THE TOP n IS TAKEN, for the reason spelled out in
// TopArtistsOwning: Rotate takes its window at start = (week * size) % len(pool),
// so a pool that comes back short moves the weekly window for every user on the
// server, including everyone whose numbering was never broken.
//
// ⛔ AND THE COUNT MATCHES HOW BuildForGenre COLLECTS, WHICH IS NOT THE SAME
// QUESTION AS FOR ARTISTS, FOR TWO SEPARATE REASONS.
//
// First, a track has more than one genre. The builder takes a track when
// HasGenre is true, and HasGenre is EqualFold over AllGenres, so a track tagged
// "Hip-Hop; Funk" really is a Funk track and a Hip-Hop track at once and must
// count for both. AllGenres has already trimmed its values and dropped
// case-duplicates within the track, so one increment per entry, keyed
// lower-case, is exactly the set of tracks HasGenre returns. Counting the
// legacy Genre field instead would rank Funk on a funk library at zero, which
// is issue #1 from Sly777 all over again.
//
// Second, and this is where counting TRACKS is still the wrong number:
// BuildForGenre hands its candidates to takeIDsCapped WITH maxPerArtist, and
// BuildForArtist does not. So a genre needs ceil(minMixSize / maxPerArtist)
// DISTINCT ARTISTS however many tracks it has. Measured: a genre with 20 tracks
// by 2 artists at the default cap of 3 yields SIX tracks, sails through a floor
// that counts tracks, and Genre Radio still starts at 2. The floor therefore
// counts CAPACITY, sum(min(tracksByArtist, maxPerArtist)), which is exactly
// what takeIDsCapped will be able to take, so the floor and the builder answer
// the same question rather than two similar ones.
//
// minOwned of 0 still means everybody passes, whatever the cap, so TopGenres
// and every existing caller are untouched.
func TopGenresOwning(tracks []Track, n, minOwned, maxPerArtist int) []string {
	plays := map[string]int{}
	// Ranking keeps its original key so TopGenres is untouched. Eligibility is
	// folded, and is per artist because the cap is per artist.
	byArtist := map[string]map[string]int{}
	for _, t := range tracks {
		// Not trimmed, because takeIDsCapped does not trim either. The floor
		// has to bucket tracks the same way the cap will.
		artist := strings.ToLower(t.Artist)
		// Every genre the track carries, not just the legacy first one. A
		// library where Funk is almost always a SECOND genre used to rank
		// Funk near zero and never build it a radio.
		for _, g := range t.AllGenres() {
			plays[g] += t.PlayCount + 1 // presence counts a little, plays count more
			key := strings.ToLower(g)
			if byArtist[key] == nil {
				byArtist[key] = map[string]int{}
			}
			byArtist[key][artist]++
		}
	}
	type gp struct {
		name string
		n    int
	}
	all := make([]gp, 0, len(plays))
	for g, c := range plays {
		if genreCapacity(byArtist[strings.ToLower(g)], maxPerArtist) < minOwned {
			continue
		}
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

// TopArtists ranks artists the same way. Every artist with at least one play
// is eligible, which is what every caller before issue #4 got.
func TopArtists(tracks []Track, n int) []string {
	return TopArtistsOwning(tracks, n, 0)
}

// TopArtistsOwning is TopArtists with a floor on how many tracks an artist must
// have IN THE CANDIDATE POOL to be eligible at all.
//
// Ranking is by plays and only by plays, which is the right answer to "who do
// you listen to" and the wrong answer to "who can fill a playlist". An artist
// with one starred track played five hundred times outranks the whole library
// and then produces a ONE track Artist Radio. One is under minMixSize, so the
// playlist is never written, and the number it would have carried is missing.
// SinTan1729 (issue #4) reported exactly that shape: Artist Radio starting at 2.
//
// ⛔ THE FILTER RUNS BEFORE THE TOP n IS TAKEN, NEVER AFTER, AND THAT ORDER IS
// THE WHOLE POINT. Rotate computes its weekly window as
// start = (week * size) % len(pool). Shortening the pool therefore moves the
// window for EVERY user on the server, including everyone this bug never
// touched. Narrowing the eligible set first and then taking n means a user who
// has n or more eligible artists still gets a pool of exactly n, so their
// rotation does not move by a single position. Only a user with fewer eligible
// artists gets a shorter pool, and that user is precisely the one whose
// numbers are missing today.
//
// Eligibility counts case-insensitively because BuildForArtist COLLECTS
// case-insensitively (strings.EqualFold, above). A case-sensitive count would
// split a library that spells one artist two ways: six "ABBA" plus six "Abba"
// is twelve tracks to the builder but two sixes to the counter, and a mix that
// works today would disappear.
func TopArtistsOwning(tracks []Track, n, minOwned int) []string {
	plays := map[string]int{}
	// Ranking keeps its original key so TopArtists is untouched; only the
	// eligibility count is folded.
	owned := map[string]int{}
	for _, t := range tracks {
		a := strings.TrimSpace(t.Artist)
		if a == "" {
			continue
		}
		plays[a] += t.PlayCount
		owned[strings.ToLower(a)]++
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
		if owned[strings.ToLower(a)] < minOwned {
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

// genreCapacity is how many tracks takeIDsCapped could actually take out of one
// genre, which is NOT the same as how many tracks the genre has.
//
// It mirrors takeIDsCapped exactly, and both quirks are deliberate: a cap of
// zero or less is no cap at all, and a BLANK artist is never capped, because
// takeIDsCapped skips its per-artist test when the key is empty. A floor that
// modelled either differently would disagree with the builder it is gating,
// which is the whole bug this function exists to close.
func genreCapacity(byArtist map[string]int, maxPerArtist int) int {
	total := 0
	for artist, n := range byArtist {
		if maxPerArtist > 0 && artist != "" && n > maxPerArtist {
			n = maxPerArtist
		}
		total += n
	}
	return total
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
		if !strings.EqualFold(strings.TrimSpace(t.Artist), anchor) {
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
		case strings.EqualFold(strings.TrimSpace(t.Artist), anchor):
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
	// The anchor is exempt from maxPerArtist, every other artist is not. See
	// takeIDsCappedExcept for why.
	//
	// ⛔ TRIMMED AS WELL AS LOWER-CASED, and both halves of that are load
	// bearing. The exemption has to recognise exactly the tracks the EqualFold
	// tests above scored as anchor tracks. Measured while fixing this: with the
	// scoring trimmed but the exemption not, an artist tagged half "Padded" and
	// half " Padded" scored all twelve tracks as anchor tracks and then had six
	// of them thrown away by the cap as if they were a guest artist, so the mix
	// came out at nine and its number went missing anyway.
	return Selection{
		Slot:     DailyMix,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCappedExcept(scored, size, maxPerArtist, strings.ToLower(strings.TrimSpace(anchor))),
	}
}

// WeekIndex is a stable rotation counter that advances once per week.
//
// It is derived from the date and nothing else, so every device and every run
// inside the same week computes the same number. That is what lets a mix
// change its contents weekly while keeping a fixed name: the identity is the
// number in the title, the content is a window into a deeper pool, and the
// window only moves when the week does.
// DayIndex is the daily rotation counter: days since the Unix epoch, plus one
// so that a real day is never the zero that means "do not rotate".
func DayIndex(now time.Time) int {
	return int(now.Unix()/(60*60*24)) + 1
}

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
