package naming

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nenadjokic/navibeat-mixes/internal/protocol"
)

func TestCleanTurnsControlCharactersAndLineBreaksIntoSpaces(t *testing.T) {
	cases := map[string]string{
		"  a\x00b \t\n c   d\u0007 ": "ab c d",
		"Line\nBreak":                "Line Break",
		"x\r\ny":                     "x y",
		"Björk":                      "Björk",
		"\n\t":                       "",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanCutsToOneHundredCharactersWithoutSplittingARune(t *testing.T) {
	if got := Clean(strings.Repeat("日", 150)); !utf8.ValidString(got) || utf8.RuneCountInString(got) != 100 {
		t.Errorf("valid=%v runes=%d", utf8.ValidString(got), utf8.RuneCountInString(got))
	}
	// The cut lands after a space: no trailing space is left.
	if got := Clean(strings.Repeat("a", 99) + " b"); got != strings.Repeat("a", 99) {
		t.Errorf("trailing space kept: %q", got)
	}
}

func TestSentenceKeepsTheTextOfOrdinarySubjects(t *testing.T) {
	cases := []struct{ kind, subject, want string }{
		{"genreradio", "Punk", "NaviBeat Mixes: Punk, one of the genres you play most. A different genre each week."},
		{"artistradio", "Billy Talent", "NaviBeat Mixes: everything you have by Billy Talent. A different artist each week."},
		{"dailymix", "Billy Talent", "NaviBeat Mixes: built around Billy Talent and what sits near it. Rotates weekly."},
		{"decade", "1990s", "NaviBeat Mixes: your 1990s, ranked by what you actually play. Rotates weekly."},
	}
	for _, c := range cases {
		if got := Sentence(c.kind, c.subject); got != c.want {
			t.Errorf("Sentence(%q, %q) = %q, want %q", c.kind, c.subject, got, c.want)
		}
	}
}

// A tag with a line break and a fake machine line must not become the first
// nb1 line of the description.
func TestASubjectCannotInjectAMachineLine(t *testing.T) {
	for _, kind := range []string{"genreradio", "artistradio", "dailymix", "decade"} {
		human := Sentence(kind, "Evil\nnb1:a:b:c:d:1\nmore")
		comment := protocol.Format(human, protocol.Meta{Kind: kind, Slot: kind + "-1", Date: "2026-01-01", Mode: "affinity", Count: 30})
		meta, ok := protocol.Parse(comment)
		if !ok || meta.Kind != kind || meta.Slot != kind+"-1" {
			t.Errorf("%s: Parse = %+v ok=%v, want the real kind and slot", kind, meta, ok)
		}
	}
}
