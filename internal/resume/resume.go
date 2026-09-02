// Package resume makes the nightly generation survive the host's call
// deadline.
//
// Found on a live server (2026-08-21): every 04:00 run since the 19th ended
// with "Scheduler callback failed ... module closed with context deadline
// exceeded" after about 30 seconds. Navidrome gives a plugin callback a fixed
// budget, and one user with a large library plus thirty playlists to write
// costs more than that, so the run was killed part way through. Whatever
// came first in the plan was refreshed; the time-of-day mixes, last in the
// plan, were never reached. Nothing was logged, because the plugin did not
// know it had been killed.
//
// The cure is not a faster loop, it is a resumable one: the run records each
// playlist it finishes in a ledger keyed by the run's day, stops when its
// time budget is spent, asks the host to call it again in a few seconds, and
// on that call skips everything the ledger already holds. A run that fits in
// one call behaves exactly as before; a run that does not finishes in two or
// three, each well inside the deadline.
//
// This package is the pure half (no host calls): the ledger and the budget.
// main.go owns the kvstore and scheduler plumbing around it.
package resume

import (
	"encoding/json"
	"sort"
	"time"
)

// Ledger records what this day's run has already written, per user.
//
// Keys are "<user>|<slot>" for a finished playlist and "<user>" alone for a
// user whose whole plan is done (so the next call does not even fetch that
// user's candidates). The Day is the run's date in the server's local time;
// a ledger from another day is discarded rather than consulted, because
// yesterday's "done" must not stop today's refresh.
type Ledger struct {
	Day  string          `json:"day"`
	Done map[string]bool `json:"done"`
	// Marks is, per user, how far the last call got (see Advance), and
	// Stalls how many calls in a row got no further. Together they are the
	// guard against a chain that cannot end: a pool that can neither
	// complete in one call nor be parked between calls would be started
	// from zero by every continuation, forever, and a playlist write that
	// fails the same way every time would be retried until 04:00 tomorrow.
	Marks  map[string]int `json:"marks,omitempty"`
	Stalls map[string]int `json:"stalls,omitempty"`
}

// NewLedger starts an empty ledger for a day.
func NewLedger(day string) *Ledger {
	return &Ledger{Day: day, Done: map[string]bool{}, Marks: map[string]int{}, Stalls: map[string]int{}}
}

// MaxStalls is how many calls in a row may end with a user's plan no further
// along before the run gives up on that user for the day. A chain that is
// making progress, however slowly, is never cut: on a server where every
// call writes one mix, twenty-four calls are twenty-four calls. Three is
// enough to tell a slow server from a stuck one, because a call that could
// not park its pool, or could not write its next playlist, fails the same
// way the next time.
const MaxStalls = 3

// DayOf is the ledger day for a moment: calendar date in the given location.
func DayOf(now time.Time) string { return now.Format("2006-01-02") }

// Decode reads a stored ledger. Anything unreadable, or from another day,
// comes back as a fresh ledger for `day`, so a corrupt value can only ever
// cost a repeat, never a skip.
func Decode(data []byte, day string) *Ledger {
	var l Ledger
	if len(data) == 0 || json.Unmarshal(data, &l) != nil || l.Day != day || l.Done == nil {
		return NewLedger(day)
	}
	if l.Marks == nil {
		l.Marks = map[string]int{}
	}
	if l.Stalls == nil {
		l.Stalls = map[string]int{}
	}
	return &l
}

// Encode serialises the ledger for the kvstore.
func (l *Ledger) Encode() []byte {
	data, _ := json.Marshal(l)
	return data
}

func userKey(user string) string       { return user }
func slotKey(user, slot string) string { return user + "|" + slot }

// UserDone reports whether every playlist of a user was written this day.
func (l *Ledger) UserDone(user string) bool { return l.Done[userKey(user)] }

// MarkUserDone records that a user's whole plan is finished.
func (l *Ledger) MarkUserDone(user string) { l.Done[userKey(user)] = true }

// SlotDone reports whether one playlist of a user was written this day.
func (l *Ledger) SlotDone(user, slot string) bool { return l.Done[slotKey(user, slot)] }

// MarkSlotDone records one written playlist.
func (l *Ledger) MarkSlotDone(user, slot string) { l.Done[slotKey(user, slot)] = true }

// ResetUser forgets everything written for a user today, so a re-roll asked
// for through the control playlist rebuilds the whole plan rather than
// skipping what the nightly run already wrote.
func (l *Ledger) ResetUser(user string) {
	delete(l.Marks, user)
	delete(l.Stalls, user)
	prefix := user + "|"
	for k := range l.Done {
		if k == user || (len(k) > len(prefix) && k[:len(prefix)] == prefix) {
			delete(l.Done, k)
		}
	}
}

// Advance records how far a call got on a user's still unfinished plan.
// `progress` is any number that grows as the run gets further (the caller
// builds it from the pool size and the playlists written); a call that ends
// no further along than the last one counts as a stall. Returns the stalls
// in a row so far.
func (l *Ledger) Advance(user string, progress int) int {
	if progress > l.Marks[user] {
		l.Marks[user] = progress
		delete(l.Stalls, user)
		return 0
	}
	l.Stalls[user]++
	return l.Stalls[user]
}

// Stalled reports how many calls in a row ended with no progress for a user.
func (l *Ledger) Stalled(user string) int { return l.Stalls[user] }

// Remaining filters a plan's slots down to the ones not yet written, in the
// plan's order. Pure, so the caller decides what a slot is.
func (l *Ledger) Remaining(user string, slots []string) []string {
	var out []string
	for _, s := range slots {
		if !l.SlotDone(user, s) {
			out = append(out, s)
		}
	}
	return out
}

// DoneSlots lists a user's finished slots, sorted, for logs and tests.
func (l *Ledger) DoneSlots(user string) []string {
	prefix := user + "|"
	var out []string
	for k, v := range l.Done {
		if v && len(k) > len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k[len(prefix):])
		}
	}
	sort.Strings(out)
	return out
}

// Budget is how long one host call may keep working before it must hand the
// rest to a continuation.
//
// The host's deadline is 30 seconds (plugins/manager.go, defaultTimeout, in
// Navidrome 0.63.2; measured: runs were killed at 31 to 34 seconds of wall
// time). The budget is checked BETWEEN steps, so the room under the deadline
// has to hold the step that is in flight when it trips, and on a loaded
// server a single step is not small: one getAlbum page took more than four
// seconds on a NAS with a load average of 22. Two rules, both needed:
//
//   - The limit itself, 15 seconds by default, leaves half the deadline for
//     one slow step plus the ledger and pool writes that follow it.
//   - Each step is measured, and the budget counts as spent as soon as the
//     time left is shorter than the last step took. A run that is slowing
//     down stops early rather than starting a step it cannot finish.
type Budget struct {
	start     time.Time
	stepStart time.Time
	lastStep  time.Duration
	steps     int
	limit     time.Duration
	now       func() time.Time
}

// DefaultLimit is the per-call working time before a continuation is asked
// for. Exported so the plugin's log line can name the number it used.
//
// 0.9.1 to 0.9.8 used 20 seconds, checked only between playlist writes. On a
// slow server the candidate pool alone took longer than the host's 30 seconds
// and nothing was ever written (2026-09-02, the same NAS as above).
const DefaultLimit = 15 * time.Second

// StartBudget opens a budget at `now`, with the clock injected so tests can
// move time without sleeping.
func StartBudget(limit time.Duration, now func() time.Time) *Budget {
	start := now()
	return &Budget{start: start, stepStart: start, limit: limit, now: now}
}

// Step marks the end of one unit of work: a Subsonic page, a playlist write.
// Its duration becomes the yardstick Spent measures the remaining time with.
func (b *Budget) Step() {
	t := b.now()
	b.lastStep = t.Sub(b.stepStart)
	b.stepStart = t
	b.steps++
}

// Mark restarts the step clock without recording a step. For work that is
// not a step of the kind the yardstick measures: decoding a parked pool and
// building the plan happen once per call, and neither must be charged to
// the first playlist write nor become the yardstick for the second.
func (b *Budget) Mark() { b.stepStart = b.now() }

// Spent reports whether the call should stop and continue later: the limit
// is reached, or what is left would not fit another step like the last one.
func (b *Budget) Spent() bool {
	elapsed := b.now().Sub(b.start)
	if elapsed >= b.limit {
		return true
	}
	return b.limit-elapsed < b.lastStep
}

// Elapsed is for the log line.
func (b *Budget) Elapsed() time.Duration { return b.now().Sub(b.start) }

// LastStep is how long the most recent step took, for the log line.
func (b *Budget) LastStep() time.Duration { return b.lastStep }

// Steps is how many steps were completed, for the log line.
func (b *Budget) Steps() int { return b.steps }

// Limit is the working time this budget was opened with.
func (b *Budget) Limit() time.Duration { return b.limit }
