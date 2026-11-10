package resume

import "testing"

// callHost is fakeHost (continue_test.go) with the two things these tests
// need on top: the delay each one-time schedule was registered with, and the
// expiring write that Watch.Arm uses.
type callHost struct {
	*fakeHost
	delays map[string]int32
}

func newCallHost() *callHost {
	return &callHost{fakeHost: newFakeHost(), delays: map[string]int32{}}
}

func (h *callHost) ScheduleOneTime(delay int32, payload, id string) (string, error) {
	got, err := h.fakeHost.ScheduleOneTime(delay, payload, id)
	if err == nil {
		h.delays[id] = delay
	}
	return got, err
}

func (h *callHost) SetWithTTL(key string, value []byte, ttl int64) error {
	return h.fakeHost.Set(key, value)
}

// hostDeadlineSec is Navidrome's per call deadline: plugins/manager.go,
// defaultTimeout, applied to every call at manager_loader.go:507 (0.64.2).
const hostDeadlineSec = 30

const dailyID = "navibeat-mixes-daily"

// killedCall is a generating call the host kills mid work: it does what
// generateAll does before the work (BeginCall, then arm the mark) and then
// nothing at all, because a killed call runs no further plugin code and no
// deferred function.
func killedCall(h *callHost, runningID string) *Call {
	c := BeginCall(h, h, "2026-11-10", 100, "generate", runningID)
	if c.Bite.GiveUp {
		return c
	}
	_ = c.Watch.Arm(h, "the candidate pool", "alice")
	return c
}

// THE 2026-11-10 REPORT (GitHub issue #18). Every daily call was killed after
// 30 to 100 s, and the log never once said "the previous call never
// returned". A killed call scheduled nothing, so the next generate call was
// the next day's 04:00 run, by which time the 15 minute mark had expired and
// the bite was full again. A call must leave a successor BEFORE it starts the
// work, and that successor must wait out the host's deadline so it does not
// read a call that is still running as a dead one.
func TestAKilledCallStillLeavesASuccessor(t *testing.T) {
	h := newCallHost()
	killedCall(h, dailyID)

	if len(h.schedules) != 1 {
		t.Fatalf("a killed call must leave exactly one successor pending, got %v", h.schedules)
	}
	for id, payload := range h.schedules {
		if payload != "generate" {
			t.Fatalf("the successor %s must generate, got payload %q", id, payload)
		}
		if h.delays[id] <= hostDeadlineSec {
			t.Fatalf("the successor %s fires after %ds, inside the host's %ds deadline, so it would race a call that is still alive",
				id, h.delays[id], hostDeadlineSec)
		}
		if int64(h.delays[id]) >= WatchTTLSec {
			t.Fatalf("the successor %s fires after %ds, after the mark has expired (%ds), so it would never see the kill",
				id, h.delays[id], WatchTTLSec)
		}
	}
}

// The successor exists only for the call that dies. A call that finishes
// leaves nothing behind, or a healthy server would get a second, empty
// generate call after every run.
func TestAFinishedCallLeavesNothingPending(t *testing.T) {
	h := newCallHost()
	c := BeginCall(h, h, "2026-11-10", 100, "generate", dailyID)
	c.Return(h, h)
	if len(h.schedules) != 0 {
		t.Fatalf("a finished call must leave nothing pending, got %v", h.schedules)
	}
	if _, ok := h.kv[ContinueKey]; ok {
		t.Fatalf("a finished call must not leave a note for a schedule that is gone")
	}
}

// A call that returns with work left hands over to the usual five second
// continuation, and the fallback goes: one successor, the fast one.
func TestAHandOverReplacesTheFallback(t *testing.T) {
	h := newCallHost()
	c := BeginCall(h, h, "2026-11-10", 100, "generate", dailyID)
	id, err := c.HandOver(h, h, 5, "generate", dailyID)
	if err != nil {
		t.Fatalf("hand over: %v", err)
	}
	c.Return(h, h)
	if len(h.schedules) != 1 {
		t.Fatalf("one successor after a hand over, got %v", h.schedules)
	}
	if _, ok := h.schedules[id]; !ok || h.delays[id] != 5 {
		t.Fatalf("the pending successor must be the 5s continuation %s, got %v (delays %v)", id, h.schedules, h.delays)
	}
	if string(h.kv[ContinueKey]) != id {
		t.Fatalf("the note must name the continuation, got %q", h.kv[ContinueKey])
	}
}

// #501871 again, through the fallback: when the note about the fallback could
// not be written, the continuation computes the fallback's own number. The
// fallback must be cancelled by its id before that, or the host refuses the
// continuation with "already exists" and the chain ends.
func TestAHandOverSurvivesALostFallbackNote(t *testing.T) {
	h := newCallHost()
	h.failSet = true
	c := BeginCall(h, h, "2026-11-10", 100, "generate", dailyID)
	if c.Fallback == "" {
		t.Fatalf("the fallback is scheduled even when its note cannot be written")
	}
	id, err := c.HandOver(h, h, 5, "generate", dailyID)
	if id == "" {
		t.Fatalf("the continuation must be scheduled, got error %v", err)
	}
	c.Return(h, h)
	if len(h.schedules) != 1 || h.delays[id] != 5 {
		t.Fatalf("one 5s successor, got %v (delays %v)", h.schedules, h.delays)
	}
}

// The whole ladder on a server where every call is killed: each fallback
// fires, reads one more kill, takes a smaller bite and arms the next one,
// until the smallest bite has been killed too. Then the chain stops, the way
// the "giving up" rung always meant, with nothing left pending.
func TestKilledCallsWalkTheLadderAndThenStop(t *testing.T) {
	h := newCallHost()
	killedCall(h, dailyID)

	var kills []int
	for i := 0; i < 10 && len(h.schedules) > 0; i++ {
		var next string
		for id := range h.schedules {
			next = id
		}
		h.Fire(next, func() {
			c := killedCall(h, next)
			kills = append(kills, c.Watch.Kills)
			if c.Bite.GiveUp && c.Fallback != "" {
				t.Fatalf("the giving up call must arm nothing, armed %s", c.Fallback)
			}
		})
	}
	want := []int{1, 2, 3, 4}
	if len(kills) != len(want) {
		t.Fatalf("kills seen by each successor = %v, want %v", kills, want)
	}
	for i := range want {
		if kills[i] != want[i] {
			t.Fatalf("kills seen by each successor = %v, want %v", kills, want)
		}
	}
	if len(h.schedules) != 0 {
		t.Fatalf("after giving up nothing may be pending, got %v", h.schedules)
	}
	if _, ok := h.kv[WatchKey]; ok {
		t.Fatalf("the giving up call clears the mark, so the next daily run starts fresh")
	}
}
