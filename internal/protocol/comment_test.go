package protocol

import (
	"strings"
	"testing"
)

// The comment field is the only channel a plugin has to a client, so its
// format is load bearing. Two rules are tested here because both protect a
// user-visible outcome:
//
//  1. The human sentence comes FIRST. `playlist.comment` is declared
//     varchar(255) and only happens not to be enforced. If a future Navidrome
//     release does enforce it, truncation must remove the machine tail and
//     leave the description a person reads, not the other way round.
//  2. The machine line is exactly one line and parses back to what was
//     written, so a client can recognise our playlists without a handshake.

// Every playlist carries one line of attribution. It is the only thing that
// tells a user of some other client where these mixes came from, so its
// absence is a regression worth failing a build over.
func TestFormatCarriesAttribution(t *testing.T) {
	got := Format("Morning mix.", Meta{Kind: "timeofday", Slot: "morning", Date: "2026-07-27", Mode: "affinity", Count: 30})
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), got)
	}
	if lines[1] != Attribution {
		t.Errorf("attribution line = %q, want %q", lines[1], Attribution)
	}
	// It must never be the first thing read, and never after the machine line.
	if lines[0] == Attribution {
		t.Error("attribution came before the sentence explaining the mix")
	}
	if !strings.HasPrefix(lines[2], "nb1:") {
		t.Errorf("machine line must stay last, got %q", lines[2])
	}
}

func TestFormatPutsHumanTextFirst(t *testing.T) {
	got := Format("Morning mix.", Meta{
		Kind: "timeofday", Slot: "morning", Date: "2026-07-27", Mode: "affinity", Count: 30,
	})
	want := "Morning mix.\n" + Attribution + "\nnb1:timeofday:morning:2026-07-27:affinity:30"
	if got != want {
		t.Fatalf("Format()\n got: %q\nwant: %q", got, want)
	}
}

func TestParseReadsBackWhatFormatWrote(t *testing.T) {
	in := Meta{Kind: "timeofday", Slot: "night", Date: "2026-07-27", Mode: "fallback", Count: 12}
	meta, ok := Parse(Format("Night mix.", in))
	if !ok {
		t.Fatal("Parse() returned ok=false for output of Format()")
	}
	if meta != in {
		t.Fatalf("round trip lost data\n got: %+v\nwant: %+v", meta, in)
	}
}

// A description whose machine tail was cut off must still be recognised as
// "not ours" rather than crashing or half-parsing into a wrong slot.
func TestParseRejectsTruncatedAndForeignComments(t *testing.T) {
	cases := map[string]string{
		"truncated tail":   "Morning mix.\nnb1:timeofday:morning",
		"no machine line":  "Just a playlist someone made by hand.",
		"empty":            "",
		"unknown schema":   "Morning mix.\nnb9:timeofday:morning:2026-07-27:affinity:30",
		"count not a number": "Morning mix.\nnb1:timeofday:morning:2026-07-27:affinity:many",
	}
	for name, in := range cases {
		if _, ok := Parse(in); ok {
			t.Errorf("%s: Parse(%q) returned ok=true, want false", name, in)
		}
	}
}
