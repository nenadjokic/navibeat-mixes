package protocol

import (
	"testing"
	"time"
)

// A command whose run was cut off by the host (the call killed mid-run) used to
// stay in the mailbox forever: the nonce was stored before the run, so every
// later poll skipped it, and no result line was ever written for the client.
func TestControlAttemptRetriesOnceThenAnswersRejected(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	timeout := 2 * time.Minute

	// First sighting: run it, and remember that a run started.
	d := DecideControlAttempt("", "n1", now, timeout)
	if d.Action != ControlRun {
		t.Fatalf("first sighting: want run, got %v", d.Action)
	}

	// Seen again while that run could still be working: leave it alone.
	d2 := DecideControlAttempt(d.Stored, "n1", now.Add(30*time.Second), timeout)
	if d2.Action != ControlSkip {
		t.Fatalf("inside the timeout: want skip, got %v", d2.Action)
	}

	// Seen again after the timeout with no result written: the run died. Try once more.
	d3 := DecideControlAttempt(d.Stored, "n1", now.Add(3*time.Minute), timeout)
	if d3.Action != ControlRun {
		t.Fatalf("after a dead run: want one retry, got %v", d3.Action)
	}

	// The retry died too: answer the client instead of looping forever.
	d4 := DecideControlAttempt(d3.Stored, "n1", now.Add(7*time.Minute), timeout)
	if d4.Action != ControlAnswerRejected {
		t.Fatalf("after two dead runs: want rejected, got %v", d4.Action)
	}
}

func TestControlAttemptFinishedCommandIsNeverRunAgain(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	done := ControlFinished("n1")
	if d := DecideControlAttempt(done, "n1", now.Add(time.Hour), time.Minute); d.Action != ControlSkip {
		t.Fatalf("finished nonce: want skip, got %v", d.Action)
	}
	// A value written by 0.9.23 and earlier is a bare nonce: treat it as finished.
	if d := DecideControlAttempt("n1", "n1", now, time.Minute); d.Action != ControlSkip {
		t.Fatalf("legacy bare nonce: want skip, got %v", d.Action)
	}
	// A new nonce always runs.
	if d := DecideControlAttempt(done, "n2", now, time.Minute); d.Action != ControlRun {
		t.Fatalf("new nonce: want run, got %v", d.Action)
	}
}
