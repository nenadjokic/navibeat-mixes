package protocol

import "testing"

func TestCommandRoundTrip(t *testing.T) {
	in := Command{Kind: CmdReroll, Slot: "morning", Nonce: "8f21"}
	line := FormatCommand(in)
	if line != "nb1:cmd:reroll:morning:8f21" {
		t.Fatalf("FormatCommand = %q", line)
	}
	got, ok := ParseCommand("Control playlist, do not delete.\n" + line)
	if !ok || got != in {
		t.Fatalf("ParseCommand = %+v ok=%v, want %+v", got, ok, in)
	}
}

// A command with no nonce would be re-executed on every single poll, which
// turns one button press into an endless regeneration loop.
func TestParseCommandRequiresANonce(t *testing.T) {
	if _, ok := ParseCommand("nb1:cmd:reroll:morning:"); ok {
		t.Error("accepted a command with an empty nonce")
	}
}

// The control playlist description is a field a user can edit by hand, so
// anything at all can end up in it. Acting on half a parsed instruction is
// worse than doing nothing.
func TestParseCommandRejectsAnythingItDoesNotFullyUnderstand(t *testing.T) {
	cases := map[string]string{
		"unknown verb":     "nb1:cmd:selfdestruct:morning:1",
		"too few fields":   "nb1:cmd:reroll:morning",
		"too many fields":  "nb1:cmd:reroll:morning:1:extra",
		"wrong schema":     "nb9:cmd:reroll:morning:1",
		"not a command":    "nb1:timeofday:morning:2026-07-27:affinity:30",
		"plain user text":  "I renamed this playlist, hope that is fine",
		"empty":            "",
	}
	for name, in := range cases {
		if _, ok := ParseCommand(in); ok {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestResultRoundTrip(t *testing.T) {
	line := FormatResult(ResultDone, "morning", "8f21")
	if line != "nb1:res:done:morning:8f21" {
		t.Fatalf("FormatResult = %q", line)
	}
	kind, slot, nonce, ok := ParseResult("Control playlist.\n" + line)
	if !ok || kind != ResultDone || slot != "morning" || nonce != "8f21" {
		t.Fatalf("ParseResult = %q %q %q ok=%v", kind, slot, nonce, ok)
	}
}

// A result line must never be mistaken for a command, or the plugin would
// answer its own reply and loop.
func TestAResultIsNotACommand(t *testing.T) {
	if _, ok := ParseCommand(FormatResult(ResultDone, "morning", "8f21")); ok {
		t.Error("the plugin's own result line parsed as a command")
	}
}
