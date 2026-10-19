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

func TestRender(t *testing.T) {
	long := strings.Repeat("x", 150)
	cases := []struct{ name, tpl, subject, want string }{
		{"empty template gives the default", "", "Billy Talent", "def"},
		{"blank template gives the default", "  ", "Billy Talent", "def"},
		{"placeholder is replaced", "This is {subject}", "Billy Talent", "This is Billy Talent"},
		{"other text is kept", "No placeholder", "Billy Talent", "No placeholder"},
		{"every placeholder is replaced", "{subject} / {subject}", "Go", "Go / Go"},
		{"placeholder is case sensitive", "{Subject} {SUBJECT}", "Go", "{Subject} {SUBJECT}"},
		{"a subject is not expanded again", "{subject}|{subject}", "{subject}", "{subject}|{subject}"},
		{"result is cleaned", "{subject}\n Radio", "Pu\x01nk", "Punk Radio"},
		{"result is cut to 100 characters", "{subject}", long, strings.Repeat("x", 100)},
		{"an empty result gives the default", "{subject}", " ", "def"},
	}
	for _, c := range cases {
		if got := Render(c.tpl, c.subject, "def"); got != c.want {
			t.Errorf("%s: Render(%q, %q) = %q, want %q", c.name, c.tpl, c.subject, got, c.want)
		}
	}
}

func TestResolveCollisions(t *testing.T) {
	long := "P " + strings.Repeat("x", MaxLen)
	other := "P " + strings.Repeat("y", MaxLen)
	fixed := func(n string) Item { return Item{Name: n, Default: n} }
	cases := []struct {
		name  string
		items []Item
		want  []string
	}{
		{"a fixed name wins when it is listed first",
			[]Item{fixed("P Morning"), {"P Morning", "P Genre Radio 1"}},
			[]string{"P Morning", "P Genre Radio 1"}},
		{"a fixed name wins when it is listed last",
			[]Item{{"P Morning", "P Genre Radio 1"}, fixed("P Morning")},
			[]string{"P Genre Radio 1", "P Morning"}},
		{"two fixed names are left alone",
			[]Item{fixed("P X"), fixed("P X")},
			[]string{"P X", "P X"}},
		{"a templated name that repeats an earlier one falls back to its default",
			[]Item{{"P Billy", "P Artist Radio 1"}, {"P Billy", "P Artist Radio 2"}, {"P Other", "P Artist Radio 3"}},
			[]string{"P Billy", "P Artist Radio 2", "P Other"}},
		{"a templated name equal to a fixed default steps aside",
			[]Item{{"P Daily Mix 1", "P Genre Radio 1"}, fixed("P Daily Mix 1"), {"P Daily Mix 1", "P Genre Radio 2"}},
			[]string{"P Genre Radio 1", "P Daily Mix 1", "P Genre Radio 2"}},
		{"a taken fallback gets (2) and then (3)",
			[]Item{fixed("P Morning"), fixed("P Genre Radio 1"), {"P Morning", "P Genre Radio 1"}, {"P Morning", "P Genre Radio 1"}},
			[]string{"P Morning", "P Genre Radio 1", "P Genre Radio 1 (2)", "P Genre Radio 1 (3)"}},
		{"the suffix is never cut off",
			[]Item{fixed(long), fixed(other), {long, other}},
			[]string{long, other, other + " (2)"}},
	}
	for _, c := range cases {
		got := ResolveCollisions(c.items)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
