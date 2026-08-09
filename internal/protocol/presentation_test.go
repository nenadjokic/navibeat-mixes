package protocol

import "testing"

const sampleMix = "NaviBeat Mixes: your morning, built from what you actually play at this time of day.\n" +
	"Made by NaviBeat  ·  navibeat.app\n" +
	"nb1:timeofday:morning:2026-07-28:affinity:30"

// THE PROMISE THAT MATTERS MOST. Every shipped NaviBeat parses the descriptor
// as exactly six fields; if adding presentation broke that, the playlist would
// stop being a mix on every device in the field.
func TestAddingAPresentationLineLeavesTheDescriptorIntact(t *testing.T) {
	comment := AppendLine(sampleMix, FormatButton(Button{Glyph: "sunrise", Color: "F2A65A", Label: "Morning Mix"}))
	m, ok := Parse(comment)
	if !ok {
		t.Fatal("descriptor stopped parsing once a presentation line was added")
	}
	if m.Kind != "timeofday" || m.Slot != "morning" || m.Count != 30 {
		t.Fatalf("descriptor changed: %+v", m)
	}
}

func TestStyleRoundTrip(t *testing.T) {
	for _, s := range []Style{StyleCover, StyleButton, StyleMosaic} {
		got, ok := ParseStyle("Control playlist, do not delete.\n" + FormatStyle(s))
		if !ok || got != s {
			t.Fatalf("style %q round trip = %q ok=%v", s, got, ok)
		}
	}
}

// Silence is not a default. A client that reads no instruction must keep
// drawing what it drew before, so ParseStyle has to distinguish "nothing said"
// from "said cover".
func TestStyleRejectsAnythingItDoesNotFullyUnderstand(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no line":          sampleMix,
		"unknown value":    "nbui1:style:carousel",
		"missing value":    "nbui1:style",
		"extra field":      "nbui1:style:button:extra",
		"wrong schema":     "nbui9:style:button",
		"plain user text":  "I renamed this playlist",
	}
	for name, in := range cases {
		if _, ok := ParseStyle(in); ok {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestButtonRoundTrip(t *testing.T) {
	in := Button{Glyph: "sunrise", Color: "F2A65A", Label: "Morning Mix"}
	got, ok := ParseButton(AppendLine(sampleMix, FormatButton(in)))
	if !ok || got != in {
		t.Fatalf("button round trip = %+v ok=%v, want %+v", got, ok, in)
	}
}

// The label is the remainder, not a field, precisely so a human name may
// contain the delimiter.
func TestALabelMayContainAColon(t *testing.T) {
	got, ok := ParseButton("nbui1:btn:radio:1A73E8:Rock: the loud half")
	if !ok || got.Label != "Rock: the loud half" {
		t.Fatalf("label = %q ok=%v", got.Label, ok)
	}
}

func TestAnEmptyLabelIsAllowed(t *testing.T) {
	got, ok := ParseButton("nbui1:btn:moon:112233:")
	if !ok || got.Label != "" || got.Glyph != "moon" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

// Asymmetric on purpose, and the asymmetry is the interesting part: an unknown
// glyph degrades to something visibly generic, while a half-read colour renders
// as an arbitrary shade with nothing to tell the user it was wrong.
func TestAnUnknownGlyphSurvivesButABadColourDoesNot(t *testing.T) {
	if got, ok := ParseButton("nbui1:btn:helicopter:F2A65A:X"); !ok || got.Glyph != "helicopter" {
		t.Errorf("an unknown glyph should still parse and be handed to the client: %+v ok=%v", got, ok)
	}
	for _, bad := range []string{
		"nbui1:btn:sun:GGGGGG:X",
		"nbui1:btn:sun:F2A6:X",
		"nbui1:btn:sun:#F2A65A:X",
		"nbui1:btn:sun",
	} {
		if _, ok := ParseButton(bad); ok {
			t.Errorf("accepted a malformed colour: %q", bad)
		}
	}
}

func TestColourIsNormalisedToUppercase(t *testing.T) {
	got, _ := ParseButton(FormatButton(Button{Glyph: "sun", Color: "f2a65a", Label: "X"}))
	if got.Color != "F2A65A" {
		t.Fatalf("colour = %q", got.Color)
	}
}

// Descriptions are rewritten daily. Appending instead of replacing would grow
// the comment past the 255 budget inside a week.
func TestRewritingReplacesTheOldLineRatherThanStacking(t *testing.T) {
	c := AppendLine(sampleMix, FormatButton(Button{Glyph: "sun", Color: "111111", Label: "A"}))
	c = AppendLine(c, FormatButton(Button{Glyph: "moon", Color: "222222", Label: "B"}))
	c = AppendLine(c, FormatButton(Button{Glyph: "star", Color: "333333", Label: "C"}))

	n := 0
	for _, line := range splitLines(c) {
		if presentationTagOf(line) == buttonTag {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d button lines survived, want 1:\n%s", n, c)
	}
	got, _ := ParseButton(c)
	if got.Label != "C" {
		t.Fatalf("kept the wrong one: %+v", got)
	}
}

// Style and button are different tags and must not evict each other, because
// the control playlist could legitimately carry both one day.
func TestTwoDifferentTagsCoexist(t *testing.T) {
	c := AppendLine(sampleMix, FormatStyle(StyleButton))
	c = AppendLine(c, FormatButton(Button{Glyph: "sun", Color: "111111", Label: "A"}))
	if _, ok := ParseStyle(c); !ok {
		t.Error("the style line was evicted by the button line")
	}
	if _, ok := ParseButton(c); !ok {
		t.Error("the button line is missing")
	}
	if _, ok := Parse(c); !ok {
		t.Error("the descriptor was lost")
	}
}

// varchar(255), declared and merely not enforced today. If this fails, the
// human sentence gets shorter, never the machine line.
func TestTheWholeDescriptionFitsTheCommentColumn(t *testing.T) {
	c := AppendLine(sampleMix, FormatButton(Button{Glyph: "sunrise", Color: "F2A65A", Label: "Morning Mix"}))
	if len(c) > 255 {
		t.Fatalf("description is %d chars, over the varchar(255) budget:\n%s", len(c), c)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
