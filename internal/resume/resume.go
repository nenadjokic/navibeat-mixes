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
}

// NewLedger starts an empty ledger for a day.
func NewLedger(day string) *Ledger {
	return &Ledger{Day: day, Done: map[string]bool{}}
}

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
	return &l
}

// Encode serialises the ledger for the kvstore.
func (l *Ledger) Encode() []byte {
	data, _ := json.Marshal(l)
	return data
}

func userKey(user string) string         { return user }
func slotKey(user, slot string) string   { return user + "|" + slot }

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
	prefix := user + "|"
	for k := range l.Done {
		if k == user || (len(k) > len(prefix) && k[:len(prefix)] == prefix) {
			delete(l.Done, k)
		}
	}
}

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
// The host's deadline is about 30 seconds (measured: runs were killed at 31
// to 34 seconds of wall time). Twenty leaves room for the write that is in
// flight when the budget trips, which is a handful of Subsonic calls and
// never more than a few seconds on any server that finishes at all.
type Budget struct {
	start time.Time
	limit time.Duration
	now   func() time.Time
}

// DefaultLimit is the per-call working time before a continuation is asked
// for. Exported so the plugin's log line can name the number it used.
const DefaultLimit = 20 * time.Second

// StartBudget opens a budget at `now`, with the clock injected so tests can
// move time without sleeping.
func StartBudget(limit time.Duration, now func() time.Time) *Budget {
	return &Budget{start: now(), limit: limit, now: now}
}

// Spent reports whether the call should stop and continue later.
func (b *Budget) Spent() bool { return b.now().Sub(b.start) >= b.limit }

// Elapsed is for the log line.
func (b *Budget) Elapsed() time.Duration { return b.now().Sub(b.start) }
