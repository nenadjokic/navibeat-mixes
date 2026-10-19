// Package naming turns a name template and a subject into a playlist name.
//
// Every mix family that can be named after what is in it has exactly one
// subject (the artist, the genre, the decade), so there is exactly one
// placeholder, {subject}. Keeping the grammar this small is the point: no
// escaping rules, no conditionals, nothing to get wrong in a settings field.
//
// This package is pure on purpose. It has no idea what a playlist is, so the
// rules a reviewer cares about (cleaning, the length cut, collisions) are
// plain functions over strings that a test can call directly.
package naming

import (
	"strconv"
	"strings"
	"unicode"
)

// Placeholder is the only token a template understands. It is case sensitive:
// "{Subject}" is ordinary text and is kept as written.
const Placeholder = "{subject}"

// MaxLen is the longest a rendered name may be, in characters. The configured
// prefix is added by the caller after rendering and is not counted.
const MaxLen = 100

// Render fills a template with the subject and cleans the result.
//
// An empty template, or one whose cleaned result is empty, gives def, which is
// the name the mix has always had. That is what makes the feature safe to
// ship: a server that never fills a template field sees no change at all.
func Render(template, subject, def string) string {
	if strings.TrimSpace(template) == "" {
		return def
	}
	out := Clean(strings.ReplaceAll(template, Placeholder, subject))
	if out == "" {
		return def
	}
	return out
}

// Clean makes a string safe to use as a playlist name: control characters are
// removed, every run of whitespace becomes one space, the ends are trimmed,
// and the result is cut to MaxLen characters.
//
// Tab, newline and the like are control characters AND whitespace. They are
// treated as whitespace (a space, then collapsed) rather than deleted, so an
// artist tag with a stray newline reads "A B" and not "AB".
//
// The cut counts runes, not bytes. Slicing bytes would split a multi-byte
// character in half and hand the server invalid UTF-8, which is a real risk
// with the artist names this plugin sees.
func Clean(s string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			pendingSpace = true
		case unicode.IsControl(r):
			// Dropped without breaking the word it sits in.
		default:
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
		}
	}
	runes := []rune(b.String())
	if len(runes) > MaxLen {
		runes = runes[:MaxLen]
	}
	return strings.TrimRightFunc(string(runes), unicode.IsSpace)
}

// Item is one mix in the ordered plan: the name it would get now, and the
// numbered default it falls back to.
type Item struct {
	// Name is the full name, prefix included.
	Name string
	// Default is the full numbered default name, prefix included.
	Default string
}

// ResolveCollisions returns the final name of every item, in order.
//
// Two mixes with the same name would be two playlists a person cannot tell
// apart. A mix whose name is fixed (Name equals Default: Morning, Daily Mix 1,
// New Music) always keeps it, exactly as upstream, and its name is reserved
// first. A mix named from a template then gives way: when its name is already
// taken it uses its numbered default instead. So a genre literally called
// "Morning" cannot push the real Morning mix to "Morning (2)" and back again
// when the weekly rotation moves.
//
// Among templated mixes the plan order decides, which is fixed, so the same
// library always resolves the same way. The default can be taken too, by a
// template that renders exactly another mix's default ("Daily Mix 1"). Then
// " (2)", " (3)" and so on is appended to the default until the name is free.
// The suffix is added to the finished name, after the 100 character cut and
// the prefix, so it can never be cut off.
func ResolveCollisions(items []Item) []string {
	taken := make(map[string]bool, len(items))
	for _, it := range items {
		if it.Name == it.Default {
			taken[it.Name] = true
		}
	}
	out := make([]string, len(items))
	for i, it := range items {
		if it.Name == it.Default {
			out[i] = it.Name
			continue
		}
		name := it.Name
		if taken[name] {
			name = it.Default
			for n := 2; taken[name]; n++ {
				name = it.Default + " (" + strconv.Itoa(n) + ")"
			}
		}
		taken[name] = true
		out[i] = name
	}
	return out
}

// Subject makes a tag value (an artist, a genre) safe to put into a playlist
// name or into the sentence of a description: control characters and line
// breaks become single spaces, and it is cut to MaxLen characters.
//
// ⛔ THE LINE BREAK IS THE POINT. The description is a human sentence, a
// newline, and the machine line, and protocol.Parse takes the FIRST line that
// starts with "nb1:". An artist tag containing "\nnb1:a:b:c:d:1" would put a
// bogus machine line ahead of the real one, so the mix could never be found
// again and a duplicate would be created on every run. Tags are written by
// whoever tagged the file, so they are untrusted text.
func Subject(s string) string { return Clean(s) }

// Sentence is the human line of the description of a mix named after a
// subject. kind is the mix family; subject is cleaned here, so a caller cannot
// forget to. An unknown kind gives just the cleaned subject.
func Sentence(kind, subject string) string {
	s := Subject(subject)
	switch kind {
	case "genreradio":
		return "NaviBeat Mixes: " + s + ", one of the genres you play most. A different genre each week."
	case "artistradio":
		return "NaviBeat Mixes: everything you have by " + s + ". A different artist each week."
	case "dailymix":
		return "NaviBeat Mixes: built around " + s + " and what sits near it. Rotates weekly."
	case "decade":
		return "NaviBeat Mixes: your " + s + ", ranked by what you actually play. Rotates weekly."
	}
	return s
}
