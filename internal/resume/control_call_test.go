package resume

import "testing"

const controlID = "navibeat-mixes-control"

// Found by the second reader of 0.9.27 before anyone ran it. A re-roll from
// the control playlist runs under the control schedule's id, so when it armed
// a fallback through Continue, Continue cancelled whatever the note named,
// which is the generate chain's pending successor. The re-roll then finished,
// withdrew its own fallback, and nothing was left pending: the chain stood
// still until the next daily run. In 0.9.26 a finished re-roll touched no
// schedule at all, and that is what it must go back to.
func TestAReRollLeavesAPendingContinuationAlone(t *testing.T) {
	h := newCallHost()
	chain := BeginCall(h, h, "2026-11-10", 100, "generate", dailyID)
	next, err := chain.HandOver(h, h, 5, "generate", dailyID)
	if err != nil {
		t.Fatalf("hand over: %v", err)
	}

	reroll := BeginControlCall(h, "2026-11-10", 100)
	reroll.Return(h, h)

	if _, ok := h.schedules[next]; !ok {
		t.Fatalf("the generate chain's continuation %s must survive a re-roll, pending now: %v", next, h.schedules)
	}
	if string(h.kv[ContinueKey]) != next {
		t.Fatalf("the note must still name %s, got %q", next, h.kv[ContinueKey])
	}
	if len(h.schedules) != 1 {
		t.Fatalf("a re-roll adds no schedule of its own, pending now: %v", h.schedules)
	}
}

// The same through a kill: a generate call is killed, and a re-roll finishes
// while its fallback is still waiting. The fallback must still fire.
func TestAReRollLeavesAKilledCallsFallbackAlone(t *testing.T) {
	h := newCallHost()
	killed := killedCall(h, dailyID)
	if killed.Fallback == "" {
		t.Fatalf("the killed call armed no fallback")
	}

	reroll := BeginControlCall(h, "2026-11-10", 100)
	reroll.Return(h, h)

	if _, ok := h.schedules[killed.Fallback]; !ok {
		t.Fatalf("the killed call's fallback %s must survive a re-roll, pending now: %v", killed.Fallback, h.schedules)
	}
	if len(h.schedules) != 1 {
		t.Fatalf("a re-roll adds no schedule of its own, pending now: %v", h.schedules)
	}
}

// The control path keeps its own retry (protocol.DecideControlAttempt), so
// it needs no fallback, but it still reads the ladder and still gives up.
func TestAReRollStillReadsTheLadder(t *testing.T) {
	h := newCallHost()
	killedCall(h, dailyID)
	reroll := BeginControlCall(h, "2026-11-10", 100)
	if reroll.Watch.Kills != 1 || reroll.Bite.Rung != 1 {
		t.Fatalf("a re-roll after a kill takes the next rung, got kills %d rung %d", reroll.Watch.Kills, reroll.Bite.Rung)
	}
	if reroll.Fallback != "" {
		t.Fatalf("a re-roll arms no fallback, armed %s", reroll.Fallback)
	}
}
