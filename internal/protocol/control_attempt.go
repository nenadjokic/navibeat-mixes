package protocol

import (
	"strconv"
	"strings"
	"time"
)

// ControlAction is what one poll does with the command it found in the mailbox.
type ControlAction int

const (
	// ControlSkip leaves the command alone: it is finished, or a run of it may still be working.
	ControlSkip ControlAction = iota
	// ControlRun executes the command.
	ControlRun
	// ControlAnswerRejected writes a rejected result without running it again.
	ControlAnswerRejected
)

// controlMaxRuns is how many times one command may start. The nonce used to be
// stored before the run, so a run the host killed mid-way left the command in
// the mailbox with no result, forever. One retry covers a host that cut a
// single call short; a second death answers the client instead of looping.
const controlMaxRuns = 2

// ControlDecision is the action plus the value to store for the user's nonce key.
type ControlDecision struct {
	Action ControlAction
	Stored string
}

// ControlFinished is the stored value for a command whose result was written.
func ControlFinished(nonce string) string { return nonce + "|done" }

// DecideControlAttempt reads the stored nonce state and the nonce now in the
// mailbox. Stored values are "nonce|done" or "nonce|run|<unix seconds>|<runs>".
// A bare nonce, written by 0.9.23 and earlier, counts as finished. timeout is how
// long a started run may be working before it is taken as dead.
func DecideControlAttempt(stored, nonce string, now time.Time, timeout time.Duration) ControlDecision {
	parts := strings.Split(stored, "|")
	if parts[0] != nonce || nonce == "" {
		return ControlDecision{Action: ControlRun, Stored: started(nonce, now, 1)}
	}
	if len(parts) < 4 || parts[1] != "run" {
		return ControlDecision{Action: ControlSkip, Stored: stored}
	}
	at, err1 := strconv.ParseInt(parts[2], 10, 64)
	runs, err2 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil {
		return ControlDecision{Action: ControlSkip, Stored: stored}
	}
	if now.Sub(time.Unix(at, 0)) < timeout {
		return ControlDecision{Action: ControlSkip, Stored: stored}
	}
	if runs >= controlMaxRuns {
		return ControlDecision{Action: ControlAnswerRejected, Stored: ControlFinished(nonce)}
	}
	return ControlDecision{Action: ControlRun, Stored: started(nonce, now, runs+1)}
}

func started(nonce string, now time.Time, runs int) string {
	return nonce + "|run|" + strconv.FormatInt(now.Unix(), 10) + "|" + strconv.Itoa(runs)
}
