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
