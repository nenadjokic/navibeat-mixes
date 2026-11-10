package resume

// A KILLED CALL HAS TO LEAVE A SUCCESSOR, AND ONLY ONE THING CAN DO THAT
// (GitHub issue #18, 0.9.27).
//
// Until 0.9.26 the only successor a call ever scheduled was the continuation
// asked for when its budget ran out, and that request is made by a call that
// RETURNS. A call the host kills runs no further plugin code, so it scheduled
// nothing, and the next generate call was the next day's 04:00 run. By then
// the watchdog mark (WatchTTLSec, 15 minutes) had long expired, NextWatch read
// no kill, and the run took the full bite again and was killed again. Reported
// from Navidrome 0.64.2: every daily call killed after 30 to 100 seconds, no
// mix changed for eight days, and "the previous call never returned" not once
// in the log, because no call ever came close enough behind a kill to see it.
//
// So the successor is scheduled BEFORE the work, as a fallback, and a call
// that returns withdraws it: a call that finished leaves nothing, a call that
// hands over replaces it with the usual five second continuation, and a call
// that is killed leaves it pending, so the next rung of the ladder runs
// FallbackDelaySec later instead of tomorrow.

// FallbackDelaySec is how long the fallback waits before it fires.
//
// It must be longer than a doomed call can live, or the fallback reads the
// mark of a call that is still working as a kill and runs next to it. The
// host's deadline is 30 seconds (Navidrome plugins/manager.go defaultTimeout,
// applied per call at manager_loader.go:507 in 0.64.2), but the kill only
// lands once the host function in flight returns, and the report behind this
// measured kills at up to 100 seconds. Navidrome gives every call its own
// instance (plugins/manager_call.go, plugin.instance per call), so nothing on
// the host side keeps two calls apart; the delay is what does.
//
// It must also be shorter than WatchTTLSec, or the mark has expired by the
// time the fallback reads it and the ladder never moves. Four rungs at two
// minutes each reach the "giving up" line about eight minutes after the
// first kill, well inside the mark's fifteen.
const FallbackDelaySec = 120

// Call is what one generating call carries from its first line to its last:
// the watchdog mark it read, the bite that mark allows, and the fallback
// successor it scheduled before starting the work.
type Call struct {
	Watch *Watch
	Bite  Bite
	// Fallback is the schedule id of the successor armed before the work,
	// or "" when none is pending on this call's behalf: nothing was armed,
	// or HandOver or Return has already withdrawn it.
	Fallback string
	// FallbackErr is why arming the fallback failed (Fallback is then ""),
	// or why its note could not be written while the schedule itself is
	// live (Fallback is then set).
	FallbackErr error
}

// BeginCall is the first thing a generating call does. It reads the watchdog
// mark the previous call left, decides the bite, and arms the fallback.
//
// When the ladder says give up, the mark is cleared and NOTHING is armed:
// that is what makes "giving up" end the chain. The call that gives up is
// usually itself a fallback, so without this it would schedule the next one
// and the chain would hammer the server forever.
//
// `runningID` is the schedule id of the callback that is asking, for the same
// reason Continue takes it (#501871).
func BeginCall(s Scheduler, st Store, day string, fullPages int, payload, runningID string) *Call {
	watch := NextWatch(st)
	bite := watch.Bite(fullPages, LoadPace(st, day))
	c := &Call{Watch: watch, Bite: bite}
	if bite.GiveUp {
		ClearWatch(st)
		return c
	}
	c.Fallback, c.FallbackErr = Continue(s, st, FallbackDelaySec, payload, runningID)
	return c
}

// HandOver schedules the continuation for a call that returns with work left,
// in place of the fallback.
//
// The fallback is cancelled by its own id FIRST, not through the note. When
// the note about the fallback could not be written (#501871), Continue numbers
// the continuation from the note and the running id alone, which is exactly
// the fallback's number, and the host would refuse it as "already exists".
func (c *Call) HandOver(s Scheduler, st Store, delay int32, payload, runningID string) (string, error) {
	c.withdraw(s, st)
	return Continue(s, st, delay, payload, runningID)
}

// Return is what every call that returns does on its way out. It withdraws
// the fallback, which is a no-op after HandOver, so a deferred Return is safe
// on every path.
func (c *Call) Return(s Scheduler, st Store) { c.withdraw(s, st) }

// withdraw cancels the fallback by its id, and forgets the note only while
// the note still names it: a note naming anything else belongs to a schedule
// that must stay findable.
func (c *Call) withdraw(s Scheduler, st Store) {
	if c.Fallback == "" {
		return
	}
	_ = s.CancelSchedule(c.Fallback)
	if noted, _ := pending(st); noted == c.Fallback {
		_ = st.Delete(ContinueKey)
	}
	c.Fallback = ""
}
