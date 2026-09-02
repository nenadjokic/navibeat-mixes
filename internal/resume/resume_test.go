package resume

import (
	"testing"
	"time"
)

func TestLedgerSkipsWhatWasWrittenAndOnlyThat(t *testing.T) {
	l := NewLedger("2026-08-21")
	l.MarkSlotDone("nenad", "morning")
	l.MarkSlotDone("nenad", "dailymix-1")
	got := l.Remaining("nenad", []string{"morning", "afternoon", "dailymix-1", "night"})
	if len(got) != 2 || got[0] != "afternoon" || got[1] != "night" {
		t.Fatalf("remaining = %v, want [afternoon night]", got)
	}
	// Another user's ledger is untouched by this one.
	if len(l.Remaining("ljubica", []string{"morning"})) != 1 {
		t.Fatal("another user's slot must not count as done")
	}
}

func TestLedgerFromAnotherDayIsDiscarded(t *testing.T) {
	old := NewLedger("2026-08-20")
	old.MarkUserDone("nenad")
	today := Decode(old.Encode(), "2026-08-21")
	if today.UserDone("nenad") {
		t.Fatal("yesterday's done must not stop today's refresh")
	}
	if today.Day != "2026-08-21" {
		t.Fatalf("day = %q", today.Day)
	}
}

func TestLedgerSurvivesARoundTripAndRejectsGarbage(t *testing.T) {
	l := NewLedger("2026-08-21")
	l.MarkSlotDone("nenad", "evening")
	l.MarkUserDone("vasa")
	back := Decode(l.Encode(), "2026-08-21")
	if !back.SlotDone("nenad", "evening") || !back.UserDone("vasa") {
		t.Fatal("round trip lost entries")
	}
	if got := back.DoneSlots("nenad"); len(got) != 1 || got[0] != "evening" {
		t.Fatalf("DoneSlots = %v", got)
	}
	if g := Decode([]byte("{not json"), "2026-08-21"); g.UserDone("vasa") || len(g.Done) != 0 {
		t.Fatal("garbage must decode to an empty ledger")
	}
}

func TestBudgetTripsAtTheLimitNotBefore(t *testing.T) {
	clock := time.Date(2026, 8, 21, 4, 0, 0, 0, time.UTC)
	b := StartBudget(20*time.Second, func() time.Time { return clock })
	if b.Spent() {
		t.Fatal("fresh budget must not be spent")
	}
	clock = clock.Add(19 * time.Second)
	if b.Spent() {
		t.Fatal("19s is inside a 20s budget")
	}
	clock = clock.Add(time.Second)
	if !b.Spent() {
		t.Fatal("20s must trip a 20s budget")
	}
	if b.Elapsed() != 20*time.Second {
		t.Fatalf("elapsed = %v", b.Elapsed())
	}
}

func TestResetUserForgetsOnlyThatUser(t *testing.T) {
	l := NewLedger("2026-08-21")
	l.MarkSlotDone("nenad", "morning")
	l.MarkUserDone("nenad")
	l.MarkUserDone("ljubica")
	l.ResetUser("nenad")
	if l.UserDone("nenad") || l.SlotDone("nenad", "morning") {
		t.Fatal("reset must forget the user's day")
	}
	if !l.UserDone("ljubica") {
		t.Fatal("reset must not touch another user")
	}
}

// One getAlbum page took more than four seconds on a loaded NAS. A budget
// that only looks at the clock would start such a page with three seconds
// left and be killed inside it; the per-step rule stops as soon as what is
// left is shorter than the last step took.
func TestBudgetStopsWhenTheLastStepWouldNotFit(t *testing.T) {
	clock := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	b := StartBudget(15*time.Second, func() time.Time { return clock })

	// Ten quick steps of a second each: 10s elapsed, 5s left, last step 1s.
	for i := 0; i < 10; i++ {
		clock = clock.Add(time.Second)
		b.Step()
	}
	if b.Spent() {
		t.Fatal("5s left and a 1s step must fit")
	}
	if b.Steps() != 10 || b.LastStep() != time.Second {
		t.Fatalf("steps = %d, last = %v", b.Steps(), b.LastStep())
	}

	// The server slows down: one step takes 4s. 14s elapsed, 1s left, and
	// the next step is expected to take 4s, so the budget is spent at 14s
	// even though the 15s limit has not been reached.
	clock = clock.Add(4 * time.Second)
	b.Step()
	if !b.Spent() {
		t.Fatalf("1s left after a 4s step must count as spent (elapsed %v)", b.Elapsed())
	}
	if b.LastStep() != 4*time.Second {
		t.Fatalf("last step = %v, want 4s", b.LastStep())
	}
}

// A budget with no steps yet behaves exactly as before: the limit alone.
func TestBudgetWithoutStepsTripsOnlyAtTheLimit(t *testing.T) {
	clock := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	b := StartBudget(15*time.Second, func() time.Time { return clock })
	clock = clock.Add(14*time.Second + 999*time.Millisecond)
	if b.Spent() {
		t.Fatal("no step has been measured, so only the limit can trip it")
	}
	clock = clock.Add(time.Millisecond)
	if !b.Spent() {
		t.Fatal("15s must trip a 15s budget")
	}
}

// The default is what the log line names and what the README promises.
func TestDefaultLimitLeavesHalfTheHostDeadline(t *testing.T) {
	if DefaultLimit != 15*time.Second {
		t.Fatalf("DefaultLimit = %v", DefaultLimit)
	}
}

// A chain that makes progress is never cut, however many calls it takes; a
// chain that gets no further three calls in a row is. The count survives the
// round trip, a reset forgets it, and an older ledger without the fields
// decodes with the counters ready.
func TestLedgerCountsStallsNotCalls(t *testing.T) {
	l := NewLedger("2026-09-02")
	for i := 1; i <= 30; i++ {
		if got := l.Advance("nenad", i); got != 0 {
			t.Fatalf("call %d made progress and was counted as stall %d", i, got)
		}
	}
	if l.Advance("nenad", 30) != 1 || l.Advance("nenad", 30) != 2 {
		t.Fatal("two calls at the same mark are two stalls")
	}
	back := Decode(l.Encode(), "2026-09-02")
	if back.Stalled("nenad") != 2 || back.Advance("nenad", 29) != 3 {
		t.Fatal("stalls did not survive the round trip, or going backwards counted as progress")
	}
	if back.Advance("nenad", 31) != 0 || back.Stalled("nenad") != 0 {
		t.Fatal("progress must clear the stalls")
	}
	back.Advance("nenad", 31)
	back.ResetUser("nenad")
	if back.Stalled("nenad") != 0 || back.Advance("nenad", 1) != 0 {
		t.Fatal("a reset must forget the marks and the stalls")
	}
	old := Decode([]byte(`{"day":"2026-09-02","done":{}}`), "2026-09-02")
	if old.Advance("nenad", 5) != 0 || old.Stalled("nenad") != 0 {
		t.Fatal("a ledger written before the fields existed must still count")
	}
}

// Decoding a parked pool and building the plan are charged to nobody: the
// first write after them is judged by the limit alone, not by their length.
func TestBudgetMarkDoesNotBecomeTheYardstick(t *testing.T) {
	clock := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	b := StartBudget(3*time.Second, func() time.Time { return clock })
	clock = clock.Add(1600 * time.Millisecond) // pool decode and plan build
	b.Mark()
	if b.Spent() || b.LastStep() != 0 {
		t.Fatalf("1.4s left with no step measured must not be spent (last step %v)", b.LastStep())
	}
	clock = clock.Add(150 * time.Millisecond) // one playlist write
	b.Step()
	if b.LastStep() != 150*time.Millisecond {
		t.Fatalf("the write was measured as %v, the mark leaked into it", b.LastStep())
	}
	if b.Spent() {
		t.Fatal("1.25s left and a 150ms yardstick must fit")
	}
}
