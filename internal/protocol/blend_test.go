package protocol

import (
	"strings"
	"testing"
)

func TestBlendLineRoundTripsAndSortsTheUsers(t *testing.T) {
	line := FormatBlend(Blend{Match: 72, Shared: 214, UserA: "nenad", UserB: "Ana"})
	if line != "nbblend1:72:214:Ana:nenad" {
		t.Fatalf("line = %q", line)
	}
	b, ok := ParseBlend("Some text\n" + line)
	if !ok || b.Match != 72 || b.Shared != 214 || b.UserA != "Ana" || b.UserB != "nenad" {
		t.Fatalf("parsed %+v ok=%v", b, ok)
	}
}

func TestBlendUsernamesWithColonsAndPercentSurvive(t *testing.T) {
	line := FormatBlend(Blend{Match: 5, Shared: 1, UserA: "a:b", UserB: "c%3Ad"})
	if strings.Count(line, ":") != 4 {
		t.Fatalf("an escaped username must not add a field: %q", line)
	}
	b, ok := ParseBlend(line)
	if !ok || b.UserA != "a:b" || b.UserB != "c%3Ad" {
		t.Fatalf("parsed %+v ok=%v from %q", b, ok, line)
	}
}

func TestBlendRejectsAnythingOffShape(t *testing.T) {
	for _, bad := range []string{
		"nbblend1:72:214:ana",         // a field short
		"nbblend1:72:214:ana:nenad:x", // a field long
		"nbblend1:101:214:ana:nenad",  // match out of range
		"nbblend1:-1:214:ana:nenad",   // negative match
		"nbblend1:72:-3:ana:nenad",    // negative shared
		"nbblend1:x:214:ana:nenad",    // not a number
		"nbblend1:72:214::nenad",      // empty user
		"nb1:blend:blend-1:2026-11-16:affinity:30",
	} {
		if _, ok := ParseBlend(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestBlendClampsWhatItWrites(t *testing.T) {
	if got := FormatBlend(Blend{Match: 140, Shared: -2, UserA: "a", UserB: "b"}); got != "nbblend1:100:0:a:b" {
		t.Fatalf("got %q", got)
	}
}

// The whole description of a Blend, long usernames included, must stay inside
// Navidrome's declared varchar(255), with the human sentence the plugin writes.
func TestBlendDescriptionStaysUnderTheCommentLimit(t *testing.T) {
	human := "NaviBeat Mixes: a Blend of what you both play, shared songs first."
	userA := strings.Repeat("a", 32)
	userB := strings.Repeat("b", 32)
	comment := Format(human, Meta{Kind: "blend", Slot: "blend-ffffffff", Date: "2026-11-16", Mode: "affinity", Count: 200})
	comment = AppendLine(comment, FormatBlend(Blend{Match: 100, Shared: 99999, UserA: userA, UserB: userB}))
	if len(comment) > 255 {
		t.Fatalf("description is %d bytes, over 255:\n%s", len(comment), comment)
	}
	if _, ok := Parse(comment); !ok {
		t.Fatal("the nb1 line no longer parses")
	}
	if _, ok := ParseBlend(comment); !ok {
		t.Fatal("the blend line no longer parses")
	}
}
