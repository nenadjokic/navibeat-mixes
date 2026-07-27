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
	ID        string
	Title     string
	Artist    string
	Genre     string
	PlayCount int
	// LastPlayed is the zero time when the track has never been played.
	LastPlayed time.Time
	Starred    bool
}

// SlotAffinity is how often a track was played inside each slot, accumulated
// by the collector. Absent entries simply score zero.
type SlotAffinity map[string]map[Slot]int

// Selection is the result of building one mix.
type Selection struct {
	Slot     Slot
	Mode     Mode
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
}

// BuildRediscover selects tracks the user clearly liked and has not heard in a
// long time. This mix needs no learning period, because last-played and play
// count are exposed per song from the moment the plugin is installed, which is
// what makes the plugin useful on the day it is installed rather than a month
// later.
func BuildRediscover(tracks []Track, opt RediscoverOptions) Selection {
	var eligible []Track
	for _, t := range tracks {
		if t.LastPlayed.IsZero() {
			// Never played is not rediscovery, it is discovery. Different mix.
			continue
		}
		liked := t.Starred || t.PlayCount >= opt.MinPlayCount
		if !liked {
			continue
		}
		age := opt.Now.Sub(t.LastPlayed)
		if age < opt.MinAge {
			continue
		}
		if age < opt.RecentGrace {
			continue
		}
		eligible = append(eligible, t)
	}

	// Oldest first, so the mix leads with what has been forgotten longest.
	// Ties break on id to keep the output deterministic, which matters
	// because an unstable order would rewrite the playlist on every run and
	// make it look like it changed when it did not.
	sort.Slice(eligible, func(i, j int) bool {
		if !eligible[i].LastPlayed.Equal(eligible[j].LastPlayed) {
			return eligible[i].LastPlayed.Before(eligible[j].LastPlayed)
		}
		return eligible[i].ID < eligible[j].ID
	})

	return Selection{
		Slot:     Rediscover,
		Mode:     ModeFallback, // Rediscover never needs the histogram.
		TrackIDs: takeIDs(eligible, opt.Size),
	}
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
		counts[strings.ToLower(t.Genre)]++
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
		if deny[strings.ToLower(t.Genre)] {
			t.Genre = ""
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
