// Package mixes holds the selection logic: given a library and what is known
// about a user's listening, decide which tracks go into which mix.
//
// Everything here is pure. It takes data in and returns track ids out, with no
// network, no host calls, and no clock of its own. That is deliberate: it is
// the part most likely to be wrong, so it has to be the part that is easiest
// to test.
package mixes

import (
	"sort"
	"strings"
	"time"
)

// Slot is one time-of-day window.
type Slot string

const (
	Morning    Slot = "morning"
	Afternoon  Slot = "afternoon"
	Evening    Slot = "evening"
	Night      Slot = "night"
	Rediscover Slot = "rediscover"
)

// TimeSlots are the four time-of-day mixes, in day order.
var TimeSlots = []Slot{Morning, Afternoon, Evening, Night}

// SlotForHour maps an hour of the day (0 to 23, server local time) onto a
// slot. Night wraps midnight, which is the only reason this is not a simple
// range check.
func SlotForHour(hour int) Slot {
	switch {
	case hour >= 5 && hour < 11:
		return Morning
	case hour >= 11 && hour < 17:
		return Afternoon
	case hour >= 17 && hour < 23:
		return Evening
	default:
		return Night
	}
}

// Mode records how a mix was built, and is shown to the user in the playlist
// description. Someone whose mixes are not yet personal deserves to know that
// is why, rather than concluding the feature does not work.
type Mode string

const (
	// ModeFallback means there is not enough observed listening yet, so the
	// mix is built from popularity and recency.
	ModeFallback Mode = "fallback"
	// ModeAffinity means the mix used the user's own hour-of-day histogram.
	ModeAffinity Mode = "affinity"
)

// Track is the subset of a song this package reasons about.
type Track struct {
	ID     string
	Title  string
	Artist string
	// Genre is the legacy single-value Subsonic field, kept because every
	// server still sends it and some send nothing else.
	Genre string
	// Genres is OpenSubsonic's `genres` array, which exists precisely because
	// a track has more than one. A track tagged "Hip-Hop; Funk" reports only
	// "Hip-Hop" in the legacy field, so a Funk radio built from `Genre` alone
	// silently skipped it. That is issue #1 from Sly777: a genre radio named
	// Funk that contained one artist, on a library full of funk.
	Genres    []string
	Year      int
	PlayCount int
	// LastPlayed is the zero time when the track has never been played.
	LastPlayed time.Time
	Starred    bool
}

// AllGenres is every genre this track carries, trimmed and without blanks.
//
// Prefers the OpenSubsonic array and falls back to the legacy single value, so
// a server that sends only `genre` behaves exactly as before. Everything that
// reasons about genre goes through here, so there is one answer to "what is
// this track's genre" rather than six.
func (t Track) AllGenres() []string {
	source := t.Genres
	if len(source) == 0 {
		source = []string{t.Genre}
	}
	out := make([]string, 0, len(source))
	seen := make(map[string]bool, len(source))
	for _, g := range source {
		g = strings.TrimSpace(g)
		if g == "" || seen[strings.ToLower(g)] {
			continue
		}
		seen[strings.ToLower(g)] = true
		out = append(out, g)
	}
	return out
}

// HasGenre is the membership test the radios need, case-insensitive.
func (t Track) HasGenre(genre string) bool {
	genre = strings.TrimSpace(genre)
	if genre == "" {
		return false
	}
	for _, g := range t.AllGenres() {
		if strings.EqualFold(g, genre) {
			return true
		}
	}
	return false
}

// sharesAnyGenre reports whether the track sits in any of the given genres,
// which are already lower-cased by the caller.
func (t Track) sharesAnyGenre(set map[string]bool) bool {
	for _, g := range t.AllGenres() {
		if set[strings.ToLower(g)] {
			return true
		}
	}
	return false
}

// SlotAffinity is how often a track was played inside each slot, accumulated
// by the collector. Absent entries simply score zero.
type SlotAffinity map[string]map[Slot]int

// Selection is the result of building one mix.
type Selection struct {
	Slot Slot
	Mode Mode
	// Relaxed reports that the preferred age window could not fill the mix and
	// a wider one was used, so the description can be honest about it.
	Relaxed  bool
	TrackIDs []string
}

// RediscoverOptions controls the Rediscover mix.
type RediscoverOptions struct {
	// Now is passed in rather than read from a clock so tests are stable.
	Now time.Time
	// MinAge is how long ago a track must last have been played.
	MinAge time.Duration
	// RecentGrace excludes anything played inside this window, so a track the
	// user just returned to is not immediately offered back to them.
	RecentGrace time.Duration
	// MinPlayCount is the play count that makes an unstarred track eligible.
	MinPlayCount int
	// Size caps the result.
	Size int
	// RelaxFloor is how recent is too recent to ever count as forgotten, no
	// matter how little else the library offers.
	RelaxFloor time.Duration
	// MinUseful is how many candidates make a mix worth building. Below this
	// the window widens rather than shipping a stub.
	MinUseful int
}

// BuildRediscover selects tracks the user clearly liked and has not heard in a
// long time. This mix needs no learning period, because last-played and play
// count are exposed per song from the moment the plugin is installed, which is
// what makes the plugin useful on the day it is installed rather than a month
// later.
//
// THE AGE THRESHOLD IS A PREFERENCE, NOT A GATE, and that was a real bug.
// A fixed "six months untouched" rule assumes a library big enough or a
// listener idle enough to have six-month-old favourites. Measured on an active
// library of 249 starred tracks, the OLDEST last play was 100 days: the default
// produced zero candidates and the user got no playlist at all, with nothing on
// screen to explain why.
//
// So the threshold is tried first, and if it cannot fill a mix the window
// relaxes to whatever the library can actually offer, down to RelaxFloor. The
// caller is told which happened (`Relaxed`) so the description can say so
// rather than quietly pretending the preference was met.
func BuildRediscover(tracks []Track, opt RediscoverOptions) Selection {
	eligible, relaxed := rediscoverPool(tracks, opt)
	return Selection{
		Slot:     Rediscover,
		Mode:     ModeFallback, // Rediscover never needs the histogram.
		Relaxed:  relaxed,
		TrackIDs: takeIDs(eligible, opt.Size),
	}
}

// rediscoverPool applies the preferred age window, then widens it if that
// cannot fill a mix. Returns the candidates and whether widening was needed.
func rediscoverPool(tracks []Track, opt RediscoverOptions) ([]Track, bool) {
	windows := []time.Duration{opt.MinAge}
	// Halve the window until it reaches the floor. Below the floor a track is
	// simply not forgotten yet, and offering it back would make the mix feel
	// like a shuffle of this week's listening.
	for w := opt.MinAge / 2; w >= opt.RelaxFloor && w > 0; w /= 2 {
		windows = append(windows, w)
	}
	if opt.RelaxFloor > 0 {
		windows = append(windows, opt.RelaxFloor)
	}

	for i, window := range windows {
		var out []Track
		for _, t := range tracks {
			if t.LastPlayed.IsZero() {
				// Never played is not rediscovery, it is discovery.
				continue
			}
			if !t.Starred && t.PlayCount < opt.MinPlayCount {
				continue
			}
			if opt.Now.Sub(t.LastPlayed) < window {
				continue
			}
			out = append(out, t)
		}
		sortOldestFirst(out)
		if len(out) >= opt.MinUseful || i == len(windows)-1 {
			return out, i > 0
		}
	}
	return nil, true
}

func sortOldestFirst(tracks []Track) {
	sort.Slice(tracks, func(i, j int) bool {
		if !tracks[i].LastPlayed.Equal(tracks[j].LastPlayed) {
			return tracks[i].LastPlayed.Before(tracks[j].LastPlayed)
		}
		return tracks[i].ID < tracks[j].ID
	})
}

// TimeMixOptions controls one time-of-day mix.
type TimeMixOptions struct {
	Slot Slot
	// Affinity is the per-track slot histogram. Empty means fallback.
	Affinity SlotAffinity
	// EventCount is how many plays have been observed for this user overall.
	EventCount int
	// MinEventsForAffinity is the threshold to leave fallback mode.
	MinEventsForAffinity int
	// Size caps the result.
	Size int
	// MaxPerArtist stops one artist from filling the mix. A mix of thirty
	// tracks by the same artist is an album, not a mix.
	MaxPerArtist int
}

// BuildTimeMix selects tracks for one time-of-day slot.
//
// Two modes, and which one ran is reported back so the description can say so.
// Below the threshold there is not enough evidence to claim a track suits a
// time of day, so the mix falls back to popularity and recency instead of
// inventing a personalisation it cannot support.
func BuildTimeMix(tracks []Track, opt TimeMixOptions) Selection {
	mode := ModeFallback
	if opt.EventCount >= opt.MinEventsForAffinity && len(opt.Affinity) > 0 {
		mode = ModeAffinity
	}

	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		s := score(t, opt.Slot, opt.Affinity, mode)
		if s <= 0 {
			continue
		}
		scored = append(scored, scoredTrack{track: t, score: s})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].track.ID < scored[j].track.ID
	})

	return Selection{
		Slot:     opt.Slot,
		Mode:     mode,
		TrackIDs: takeIDsCapped(scored, opt.Size, opt.MaxPerArtist),
	}
}

type scoredTrack struct {
	track Track
	score int
}

func score(t Track, slot Slot, aff SlotAffinity, mode Mode) int {
	if mode == ModeAffinity {
		// Plays inside this slot are the whole point, so they dominate. A
		// small share of overall popularity breaks ties between tracks with
		// equal slot evidence.
		inSlot := aff[t.ID][slot]
		if inSlot == 0 {
			return 0
		}
		return inSlot*10 + min(t.PlayCount, 10)
	}
	// Fallback: popularity, with a nudge for anything explicitly loved.
	s := t.PlayCount
	if t.Starred {
		s += 3
	}
	return s
}

// FilterGenres drops tracks whose genre carries no information: an explicit
// denylist, plus any genre covering more than `threshold` of the library.
// The second rule matters more than it looks. A real library had one genre on
// 15316 of 15520 tracks, an artifact of a tagging tool, and selecting on it
// would have been the same as selecting at random while looking deliberate.
func FilterGenres(tracks []Track, denylist []string, threshold float64) []Track {
	if len(tracks) == 0 {
		return tracks
	}
	deny := make(map[string]bool, len(denylist))
	for _, g := range denylist {
		deny[strings.ToLower(strings.TrimSpace(g))] = true
	}

	counts := map[string]int{}
	for _, t := range tracks {
		for _, g := range t.AllGenres() {
			counts[strings.ToLower(g)]++
		}
	}
	total := float64(len(tracks))
	for g, n := range counts {
		if g != "" && float64(n)/total > threshold {
			deny[g] = true
		}
	}

	out := make([]Track, 0, len(tracks))
	for _, t := range tracks {
		// A track is kept, only its genre stops being usable as a signal.
		// Dropping the track entirely would empty the library on exactly the
		// servers this rule exists for.
		// Strip only the genres that carry no information, and keep the rest.
		// The old code blanked the whole track's genre when its ONE value was
		// denied, which on a multi-genre library threw away the useful values
		// alongside the useless one.
		kept := make([]string, 0, len(t.Genres))
		for _, g := range t.AllGenres() {
			if !deny[strings.ToLower(g)] {
				kept = append(kept, g)
			}
		}
		t.Genres = kept
		if len(kept) == 0 {
			t.Genre = ""
		} else {
			t.Genre = kept[0]
		}
		out = append(out, t)
	}
	return out
}

func takeIDs(tracks []Track, size int) []string {
	if size > 0 && len(tracks) > size {
		tracks = tracks[:size]
	}
	ids := make([]string, 0, len(tracks))
	for _, t := range tracks {
		ids = append(ids, t.ID)
	}
	return ids
}

func takeIDsCapped(scored []scoredTrack, size, maxPerArtist int) []string {
	ids := make([]string, 0, size)
	perArtist := map[string]int{}
	for _, s := range scored {
		if size > 0 && len(ids) >= size {
			break
		}
		if maxPerArtist > 0 {
			key := strings.ToLower(s.track.Artist)
			if key != "" && perArtist[key] >= maxPerArtist {
				continue
			}
			perArtist[key]++
		}
		ids = append(ids, s.track.ID)
	}
	return ids
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
