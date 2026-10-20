// Package naming cleans text that comes from tags before it is used in a
// playlist description.
//
// This package is pure on purpose. It has no idea what a playlist is, so the
// rules a reviewer cares about (cleaning, the length cut) are plain functions
// over strings that a test can call directly.
package naming

import (
	"strings"
	"unicode"
)

// MaxLen is the longest a cleaned subject may be, in characters.
const MaxLen = 100

// Clean makes a string safe to use in a playlist name or description: control characters are
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
