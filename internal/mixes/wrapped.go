package mixes

import (
	"sort"
	"strconv"
	"time"
)

// Wrapped is the yearly recap: the tracks a user actually played most, ranked.
//
// It is built from the collector's own observations rather than from the
// library's play counts, and that difference matters. A library play count is
// all-time and includes every year; the histogram only holds what this plugin
// watched. So a recap says "since the plugin was installed", and the
// description has to say so rather than implying it knows about a year it was
// not present for.

// WrappedOptions controls the recap.
type WrappedOptions struct {
	// Plays maps a track id to how many plays the collector observed.
	Plays map[string]int
	// Size caps the recap.
	Size int
	// MaxPerArtist stops one artist owning the whole year.
	MaxPerArtist int
}

// BuildWrapped ranks tracks by observed plays.
func BuildWrapped(tracks []Track, opt WrappedOptions) Selection {
	scored := make([]scoredTrack, 0, len(tracks))
	for _, t := range tracks {
		n := opt.Plays[t.ID]
		if n <= 0 {
			continue
		}
		scored = append(scored, scoredTrack{track: t, score: n})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].track.ID < scored[j].track.ID
	})

	return Selection{
		Slot:     Slot("wrapped"),
		Mode:     ModeAffinity, // it is built entirely from observed listening
		TrackIDs: takeIDsCapped(scored, opt.Size, opt.MaxPerArtist),
	}
}

// WrappedName is the playlist name for a given year. This is the one place a
// playlist name legitimately carries variable data, because a new year really
// is a new playlist: last year's recap should stay on the shelf rather than
// being overwritten by this year's.
func WrappedName(year int) string {
	return "Wrapped " + strconv.Itoa(year)
}

// WrappedSlot is the slot key used in the machine line, kept distinct per year
// so a client can tell two recaps apart.
func WrappedSlot(year int) string {
	return "wrapped-" + strconv.Itoa(year)
}

// YearOf returns the calendar year of a time, in server local time.
func YearOf(t time.Time) int { return t.Year() }
