package resume

import (
	"strconv"
	"strings"
)

// Scheduler is the slice of the host's scheduler a continuation needs. The
// plugin passes the real host functions; tests pass a fake that behaves the
// way Navidrome's does.
type Scheduler interface {
	ScheduleOneTime(delaySeconds int32, payload, scheduleID string) (string, error)
	CancelSchedule(scheduleID string) error
}

// Store is the slice of the kvstore a continuation needs: one key that
// remembers the id of the continuation currently pending.
type Store interface {
	Get(key string) ([]byte, bool, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// ContinueKey is where the pending continuation's schedule id is kept.
const ContinueKey = "run:continue"

// ContinuePrefix is the fixed part of every continuation's schedule id, so
// the host's log lines for them can be found with one search.
const ContinuePrefix = "navibeat-mixes-continue"

// Continue asks the host for one more "generate" call in `delay` seconds and
// returns the schedule id it registered.
//
// ⛔ THE ID IS NEW EVERY TIME, AND A FIXED ID WAS THE BUG. 0.9.1 to 0.9.8
// scheduled every continuation as "navibeat-mixes-continue". Read from
// Navidrome 0.63.2, plugins/host_scheduler.go:
//
//	ScheduleOneTime  refuses an id that is already in its map ("already
//	                 exists", line 71), and
//	the timer's func  runs the callback FIRST and deletes the id from the map
//	                 AFTER the callback returns (lines 76 to 82).
//
// So while a continuation is running, its own id is still registered, and
// the continuation it tries to schedule for the rest of the work is refused.
// That is the 2026-08-31 log line "could not schedule the continuation:
// schedule ID navibeat-mixes-continue already exists", and it is why a run
// that needs three calls only ever got two.
//
// Cancelling the pending id first is not enough on its own: the cleanup after
// the callback deletes whatever sits under the running id, so a fresh entry
// registered under the same id would be dropped from the map and its timer
// would fire into "Schedule entry not found". A numbered id sidesteps both:
// the previous continuation is cancelled (harmless when it is the one running
// right now, or when the host has already forgotten it), and the new one is
// registered under an id the cleanup will not touch.
// `runningID` is the schedule id of the callback that is asking, straight
// from the host, or "" when the caller is not inside one (the control path,
// or a test).
//
// ⛔ WHY THE RUNNING ID IS A PARAMETER (#501871, found by the second reader of
// 0.9.9). The number used to come from the store alone, and the store is
// written AFTER the schedule is registered, so a kvstore Set that fails leaves
// the note one number behind while the schedule under the new number is live.
// The next continuation then computed that same number again, and the host
// refused it with "already exists" because the running id was still in its
// map, so one failed write ended the chain. Deriving the successor from BOTH
// the note and the id that is running makes the collision unreachable: the
// running number is known first-hand, whatever the store says.
func Continue(s Scheduler, st Store, delay int32, payload, runningID string) (string, error) {
	previous, n := pending(st)
	if r := numberOf(runningID); r > n {
		n = r
	}
	// The id we are running under is never cancelled: the host deletes it
	// itself once this callback returns, and cancelling it here would only
	// race that cleanup.
	if previous != "" && previous != runningID {
		// A pending one is superseded, so two continuations never race for
		// the same ledger. "not found" is the usual answer here (the host
		// forgets a one-time schedule once it has fired) and is not an error.
		_ = s.CancelSchedule(previous)
	}
	next := ContinuePrefix + "-" + strconv.Itoa(n+1)
	if _, err := s.ScheduleOneTime(delay, payload, next); err != nil {
		return "", err
	}
	if err := st.Set(ContinueKey, []byte(next)); err != nil {
		// The schedule is registered whatever happens to the note about it.
		// Without the note the next Continue cannot cancel this one, which
		// costs at worst one duplicate pass over a ledger that skips what is
		// done, so the call still succeeds.
		return next, err
	}
	return next, nil
}

// CancelStale cancels the continuation remembered in the store, if any, and
// forgets it. Called when the plugin loads: the host drops every schedule on
// unload, so what the store remembers is at best a stale id, and the next
// Continue must not count on it.
//
// Returns the id it cancelled, or "" when there was nothing to cancel.
func CancelStale(s Scheduler, st Store) string {
	previous, _ := pending(st)
	if previous == "" {
		return ""
	}
	_ = s.CancelSchedule(previous)
	_ = st.Delete(ContinueKey)
	return previous
}

// numberOf reads the trailing number of a continuation id, or 0 for anything
// that is not one: the daily schedule's id, an empty string, a corrupt value.
func numberOf(id string) int {
	if !strings.HasPrefix(id, ContinuePrefix+"-") {
		return 0
	}
	n, err := strconv.Atoi(id[len(ContinuePrefix)+1:])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// pending reads the remembered continuation id and its number. A value that
// does not carry the prefix is treated as no continuation, so a corrupt key
// can only ever cost a stale entry left in the host's map, never a stuck run.
func pending(st Store) (string, int) {
	data, ok, err := st.Get(ContinueKey)
	if err != nil || !ok {
		return "", 0
	}
	id := string(data)
	if !strings.HasPrefix(id, ContinuePrefix+"-") {
		return "", 0
	}
	n, err := strconv.Atoi(id[len(ContinuePrefix)+1:])
	if err != nil || n < 0 {
		return "", 0
	}
	return id, n
}
