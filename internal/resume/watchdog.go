package resume

import "encoding/json"

// The watchdog survives the one thing the budget cannot: a call the host
// killed.
//
// Budget stops the plugin BETWEEN units of work. It is read after a Subsonic
// page, never during one, and a host call already in flight cannot be
// interrupted from inside the plugin: host.SubsonicAPICall is a host
// function, the plugin holds no client and no context.
//
// So on a machine where a SINGLE call takes longer than the host's 30 second
// deadline, the plugin is killed before it ever reaches the line that reads
// its own budget, and budgetSeconds has no effect whatsoever. Reported on
// 2026-09-13 from a Raspberry Pi 3B running Navidrome 0.64.0: the callback
// died at 30.79s with budgetSeconds set to 3.
//
// A killed call is also a call that wrote nothing. The ledger is saved after
// generateForUser returns, so a kill never advances Stalls, and MaxStalls
// cannot end a chain that dies this way. Every continuation then repeats the
// same too big bite, forever, and nothing in the store says it ever happened.
//
// The mark below is written BEFORE the work and cleared on every path that
// returns normally, which is exactly what makes it evidence: finding it set
// at the start of a call means the previous call did not return. Each
// consecutive kill takes the next rung down the ladder, and when the
// smallest bite is still killed the run says so once and stops asking for
// continuations instead of hammering a machine that cannot serve it.
const (
	// WatchKey is where the mark lives.
	WatchKey = "run:watch"
	// PaceKey remembers the smallest bite that has been seen to SURVIVE on
	// this server today, which the mark above cannot: the mark is cleared by
	// every call that returns, and that is the call which proved the bite
	// works. Without this the next call would go back to the full bite and
	// be killed again, so a slow server would spend two killed calls for
	// every call that gets anything done, forever.
	PaceKey = "run:pace"
	// PaceTTLSec is storage hygiene only. What actually releases the pace is
	// the day changing, the same rule the ledger uses: tomorrow's run is
	// owed a fresh look at a server that may have been given more memory, a
	// faster disk, or simply a finished scan.
	PaceTTLSec = 24 * 60 * 60
	// WatchTTLSec expires a mark left by a chain that is long dead: the
	// plugin was disabled mid call, or the server went down. A continuation
	// follows five seconds behind the call that asked for it, so anything
	// this old belongs to no chain that is still running, and the next run
	// is owed a full bite rather than the last one's punishment. The day is
	// deliberately NOT part of the key: a call killed at 23:59 and its
	// continuation four seconds later are the same chain.
	WatchTTLSec = 15 * 60
)

// TTLStore is Store plus the expiring write the mark needs.
type TTLStore interface {
	Store
	SetWithTTL(key string, value []byte, ttlSeconds int64) error
}

// Watch is the mark one call leaves in the store while it works.
type Watch struct {
	// Kills is how many calls in a row were killed before this one. Zero on
	// a healthy server, because the previous call cleared its own mark.
	Kills int `json:"k"`
	// Phase and User are where the work was when the mark was written, so
	// the log line after a kill can name what died rather than only that
	// something did.
	Phase string `json:"p,omitempty"`
	User  string `json:"u,omitempty"`
}

// NextWatch reads the mark the previous call left and returns the one this
// call should arm itself with. A mark that is still there is a call that
// never returned, so its Kills is carried forward plus one, and its Phase
// and User are kept until Arm overwrites them with this call's own.
func NextWatch(s Store) *Watch {
	data, ok, err := s.Get(WatchKey)
	if err != nil || !ok || len(data) == 0 {
		return &Watch{}
	}
	var w Watch
	if json.Unmarshal(data, &w) != nil {
		// An unreadable mark is still a mark: something wrote it and did not
		// come back to clear it. Counting it as one kill is the reading that
		// cannot hide the failure.
		return &Watch{Kills: 1}
	}
	w.Kills++
	return &w
}

// Arm writes the mark for the work about to start. It is called before the
// work and again at each phase boundary, so a kill leaves behind the phase
// it happened in.
func (w *Watch) Arm(s TTLStore, phase, user string) error {
	w.Phase, w.User = phase, user
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return s.SetWithTTL(WatchKey, data, WatchTTLSec)
}

// ClearWatch removes the mark, which is what "this call returned" means.
func ClearWatch(s Store) { _ = s.Delete(WatchKey) }

// Pace is the rung that has been seen to work on this server today.
type Pace struct {
	Day  string `json:"d"`
	Rung int    `json:"r"`
}

// LoadPace is the remembered rung for today, or zero when there is none and
// when the one in the store belongs to another day.
func LoadPace(s Store, day string) int {
	data, ok, err := s.Get(PaceKey)
	if err != nil || !ok || len(data) == 0 {
		return 0
	}
	var p Pace
	if json.Unmarshal(data, &p) != nil || p.Day != day || p.Rung < 0 {
		return 0
	}
	if p.Rung > len(biteLadder)-1 {
		return len(biteLadder) - 1
	}
	return p.Rung
}

// SavePace remembers a rung that survived. It is called on the way out of a
// call that RETURNED, which is the only evidence that a bite fits.
func SavePace(s TTLStore, day string, rung int) error {
	if rung <= 0 {
		return nil
	}
	data, err := json.Marshal(Pace{Day: day, Rung: rung})
	if err != nil {
		return err
	}
	return s.SetWithTTL(PaceKey, data, PaceTTLSec)
}

// ClearPace forgets the remembered rung, for a load, which is a fresh start.
func ClearPace(s Store) { _ = s.Delete(PaceKey) }

// Bite is how much a call allows itself to bring back in one go.
type Bite struct {
	// Rung is which step of the ladder this bite is, so the caller can
	// remember it when the call returns without recomputing anything.
	Rung int
	// AlbumPages is the size asked of getAlbumList2, so a smaller bite is a
	// smaller response for the host to marshal and for the plugin to parse.
	AlbumPages int
	// SkipStarred folds the pool's one unpaginated call out of the run. It
	// is the last rung before giving up because getStarred2 takes no size
	// parameter in the Subsonic API, so it is the one call on this path that
	// cannot be made smaller by asking differently. Album fetches still
	// carry each track's starred flag, so the loved mix is thinner on that
	// run rather than empty.
	SkipStarred bool
	// GiveUp means every rung has been killed: one host call on this server
	// outlasts the host's deadline, and no bite this plugin can take will
	// fit inside it. The run says so and stops the chain.
	GiveUp bool
}

// biteLadder is one rung per consecutive kill: the divisor applied to the
// full album page size, and whether the unpaginated call is skipped. The
// steps are deliberately steep rather than gradual. Each rung costs the
// reporter another killed call plus a five second continuation, so halving
// would spend six calls to reach what these reach in three.
var biteLadder = []struct {
	pagesDiv    int
	skipStarred bool
}{
	{1, false},  // no kills: the full bite
	{4, false},  // one kill
	{20, false}, // two kills
	{20, true},  // three kills: and without getStarred2
}

// Bite is the rung this call is on: the floor that already survived today,
// plus one step down for every consecutive kill since. `fullPages` is the
// page size a healthy server would be asked for.
//
// The two add rather than compete. The floor is what this server has been
// SEEN to manage, so starting above it would be asking for a bite that was
// already too big; the kills are what has happened since, so ignoring them
// would be asking for it again.
func (w *Watch) Bite(fullPages, floor int) Bite {
	if floor < 0 {
		floor = 0
	}
	rung := floor + w.Kills
	if rung >= len(biteLadder) {
		return Bite{Rung: len(biteLadder), AlbumPages: 1, SkipStarred: true, GiveUp: true}
	}
	step := biteLadder[rung]
	pages := fullPages / step.pagesDiv
	if pages < 1 {
		pages = 1
	}
	return Bite{Rung: rung, AlbumPages: pages, SkipStarred: step.skipStarred}
}
