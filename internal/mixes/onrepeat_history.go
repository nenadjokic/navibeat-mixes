package mixes

import (
	"sort"
	"strconv"
	"time"
)

// Play is one entry of the server's own play history: which track, and when.
// Navidrome 0.64.0 and newer hands these to a plugin through its
// ScrobbleRetriever host service (a ScrobbleRef carries a media file id and a
// submission time, nothing else), and the media file id is the same id the
// Subsonic API gives a song, so it matches Track.ID directly.
type Play struct {
	TrackID string
	At      time.Time
}

// Tally is what the history says about one track: how many distinct plays,
// and when the latest one was.
type Tally struct {
	Count int
	Last  time.Time
}

// playDedupFloor is the collector's minDedupWindow
// (internal/collector/histogram.go). The two must agree, or the same stuck
// client would count once in the histogram and 46 times in On Repeat.
const playDedupFloor = 60 * time.Second

// TallyPlays counts distinct plays per track with the collector's rule: the
// same track inside max(60s, track duration) of the previous counted play is
// the same play. Plays may arrive in any order; they are sorted first, so a
// newest-first page dedups exactly like an oldest-first one.
func TallyPlays(plays []Play, durations map[string]time.Duration) map[string]Tally {
	sorted := make([]Play, 0, len(plays))
	for _, p := range plays {
		if p.TrackID == "" || p.At.IsZero() {
			continue
		}
		sorted = append(sorted, p)
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	out := map[string]Tally{}
	for _, p := range sorted {
		window := durations[p.TrackID]
		if window < playDedupFloor {
			window = playDedupFloor
		}
		t, seen := out[p.TrackID]
		if seen && p.At.Sub(t.Last) < window {
			continue
		}
		t.Count++
		t.Last = p.At
		out[p.TrackID] = t
	}
	return out
}

// BuildOnRepeatFromPlays is On Repeat ranked by what the user actually played
// inside the window, from the server's own history, instead of by the
// lifetime play count (which let a song played twice this month outrank one
// played every day, as moop250 noticed). A track needs at least two distinct
// plays inside the window, the same bar BuildOnRepeat sets with
// PlayCount >= 2. Equal counts break on the most recent play, then on id.
func BuildOnRepeatFromPlays(tracks []Track, plays []Play, now time.Time, window time.Duration, size, maxPerArtist int) Selection {
	byID := make(map[string]Track, len(tracks))
	durations := make(map[string]time.Duration, len(tracks))
	for _, t := range tracks {
		byID[t.ID] = t
		if t.Duration > 0 {
			durations[t.ID] = t.Duration
		}
	}
	inWindow := make([]Play, 0, len(plays))
	for _, p := range plays {
		if p.At.After(now) || now.Sub(p.At) > window {
			continue
		}
		inWindow = append(inWindow, p)
	}

	type ranked struct {
		track Track
		Tally
	}
	var list []ranked
	for id, t := range TallyPlays(inWindow, durations) {
		tr, ok := byID[id]
		// A play for a track outside the candidate pool (another library, a
		// deleted file) cannot go into the playlist.
		if !ok || t.Count < 2 {
			continue
		}
		list = append(list, ranked{track: tr, Tally: t})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		if !list[i].Last.Equal(list[j].Last) {
			return list[i].Last.After(list[j].Last)
		}
		return list[i].track.ID < list[j].track.ID
	})
	scored := make([]scoredTrack, 0, len(list))
	for _, r := range list {
		scored = append(scored, scoredTrack{track: r.track, score: r.Count})
	}
	return Selection{
		Slot:     OnRepeat,
		Mode:     ModeFallback,
		TrackIDs: takeIDsCapped(scored, size, maxPerArtist),
	}
}

// PlayHistory is the result of reading the server's play history for one
// user: the plays, or the error that stopped the read.
type PlayHistory struct {
	Plays []Play
	Err   error
}

// OnRepeatInput is everything PlanOnRepeat needs.
type OnRepeatInput struct {
	Tracks       []Track
	Now          time.Time
	Window       time.Duration
	Size         int
	MaxPerArtist int
	// History is nil when this build does not read the server's history at
	// all. Then On Repeat is exactly what it has always been, description
	// included.
	History *PlayHistory
}

// onRepeatClassicDescription is the description On Repeat has carried since
// it was added. It stays for a build that does not read the history.
const onRepeatClassicDescription = "NaviBeat Mixes: what you have been playing over and over lately."

// OnRepeatAllTimeDescription is said when the history was asked for and could
// not be read, so the mix fell back to the lifetime play count.
//
// It does NOT say "your server is older than 0.64", although that was the
// first idea: a build that asks for the history cannot even load on a server
// older than 0.64.0 (history_none.go explains why), so whoever reads this
// description is on 0.64 or newer and the read failed for another reason.
const OnRepeatAllTimeDescription = "NaviBeat Mixes: what you have been playing lately, ranked by all-time plays because the server's play history could not be read on this run."

func onRepeatHistoryDescription(window time.Duration) string {
	days := int(window / (24 * time.Hour))
	return "NaviBeat Mixes: the songs you played most in the last " + strconv.Itoa(days) + " days."
}

// PlanOnRepeat picks the On Repeat ranking and the description that tells
// the truth about it.
func PlanOnRepeat(in OnRepeatInput) (Selection, string) {
	switch {
	case in.History == nil:
		return BuildOnRepeat(in.Tracks, in.Now, in.Window, in.Size, in.MaxPerArtist), onRepeatClassicDescription
	case in.History.Err != nil:
		return BuildOnRepeat(in.Tracks, in.Now, in.Window, in.Size, in.MaxPerArtist), OnRepeatAllTimeDescription
	default:
		return BuildOnRepeatFromPlays(in.Tracks, in.History.Plays, in.Now, in.Window, in.Size, in.MaxPerArtist),
			onRepeatHistoryDescription(in.Window)
	}
}
