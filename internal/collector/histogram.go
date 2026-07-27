// Package collector turns play events into the histogram the time-of-day
// mixes are built from.
//
// It exists because the Subsonic API exposes only a track's LAST play, never
// the individual plays. Hour-of-day affinity cannot be derived from the API at
// all, so the only way to know that someone listens to a particular record in
// the morning is to watch the plays go past and remember.
//
// The state machine is small and lives here in pure form so it can be tested
// without a host, a database, or a clock.
package collector

import (
	"encoding/json"
	"time"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

// Event is one observed play.
type Event struct {
	TrackID  string
	Artist   string
	At       time.Time
	Duration time.Duration
}

// State is everything the collector remembers for one user. It is stored as
// JSON in the host key-value store.
type State struct {
	// Hours counts plays by hour of day, 0 to 23.
	Hours [24]int `json:"hours"`
	// Weekdays counts plays by weekday, Sunday first.
	Weekdays [7]int `json:"weekdays"`
	// Tracks maps a track id to its per-slot counts.
	Tracks map[string]map[string]int `json:"tracks"`
	// Events is the total number of accepted plays, which is what decides
	// whether the mixes have enough evidence to leave fallback mode.
	Events int `json:"events"`
	// LastSeen is the last accepted play per track, kept only for dedup.
	LastSeen map[string]int64 `json:"lastSeen"`
}

// NewState returns an empty state with its maps ready.
func NewState() *State {
	return &State{Tracks: map[string]map[string]int{}, LastSeen: map[string]int64{}}
}

// minDedupWindow is the floor for the duplicate window. Very short tracks
// would otherwise get a window so small that a burst of repeats all count.
const minDedupWindow = 60 * time.Second

// Accept records a play and reports whether it counted.
//
// DUPLICATE SUBMISSIONS ARE REAL AND MUST BE FILTERED. Measured on a live
// server, roughly a quarter of all scrobbles were duplicates, and one track
// arrived 46 times inside a single second. Feeding that straight into the
// histogram would let one stuck client decide what the user's morning sounds
// like. The rule: the same track from the same user inside
// max(60s, track duration) is the same play, not a new one.
func (s *State) Accept(e Event) bool {
	if e.TrackID == "" || e.At.IsZero() {
		return false
	}
	window := e.Duration
	if window < minDedupWindow {
		window = minDedupWindow
	}
	if last, ok := s.LastSeen[e.TrackID]; ok {
		if e.At.Sub(time.Unix(last, 0)) < window {
			return false
		}
	}

	s.LastSeen[e.TrackID] = e.At.Unix()
	s.Hours[e.At.Hour()]++
	s.Weekdays[int(e.At.Weekday())]++
	s.Events++

	slot := string(mixes.SlotForHour(e.At.Hour()))
	if s.Tracks[e.TrackID] == nil {
		s.Tracks[e.TrackID] = map[string]int{}
	}
	s.Tracks[e.TrackID][slot]++
	return true
}

// Affinity projects the stored counts into the shape the selection logic
// expects.
func (s *State) Affinity() mixes.SlotAffinity {
	out := make(mixes.SlotAffinity, len(s.Tracks))
	for id, slots := range s.Tracks {
		m := make(map[mixes.Slot]int, len(slots))
		for slot, n := range slots {
			m[mixes.Slot(slot)] = n
		}
		out[id] = m
	}
	return out
}

// TotalPlays returns observed plays per track, which is what the yearly recap
// is ranked by.
func (s *State) TotalPlays() map[string]int {
	out := make(map[string]int, len(s.Tracks))
	for id, slots := range s.Tracks {
		total := 0
		for _, n := range slots {
			total += n
		}
		out[id] = total
	}
	return out
}

// Prune keeps the state from growing without bound on a large library, by
// dropping the least-played tracks once there are more than `cap` of them.
// Without this, a library of tens of thousands of tracks would eventually
// store an entry for every one of them.
func (s *State) Prune(cap int) {
	if cap <= 0 || len(s.Tracks) <= cap {
		return
	}
	type entry struct {
		id string
		n  int
	}
	all := make([]entry, 0, len(s.Tracks))
	for id, slots := range s.Tracks {
		total := 0
		for _, n := range slots {
			total += n
		}
		all = append(all, entry{id, total})
	}
	// Partial ordering is enough: find the threshold, drop below it. Ties are
	// broken by id so pruning is deterministic.
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].n > all[i].n || (all[j].n == all[i].n && all[j].id < all[i].id) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	for _, e := range all[cap:] {
		delete(s.Tracks, e.id)
		delete(s.LastSeen, e.id)
	}
}

// Marshal serialises the state for the key-value store.
func (s *State) Marshal() ([]byte, error) { return json.Marshal(s) }

// Unmarshal restores state. A stored value that no longer parses, because an
// older build wrote a different shape, yields a fresh state rather than an
// error: losing a histogram costs a warm-up period, while failing hard would
// stop the plugin producing anything at all.
func Unmarshal(data []byte) *State {
	if len(data) == 0 {
		return NewState()
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return NewState()
	}
	if s.Tracks == nil {
		s.Tracks = map[string]map[string]int{}
	}
	if s.LastSeen == nil {
		s.LastSeen = map[string]int64{}
	}
	return &s
}
