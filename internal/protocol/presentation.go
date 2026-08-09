package protocol

import "strings"

// The presentation half of the wire format: how a NaviBeat client should DRAW
// these playlists, decided here on the server rather than by a setting in each
// app.
//
// WHY THE SERVER DECIDES. A setting inside NaviBeat for a feature that lives in
// this plugin is a dead switch for everyone who never installed the plugin.
// That is not hypothetical: NaviBeat ships a "NaviBeat covers for generated
// mixes" toggle in Settings unconditionally, and on a server without this
// plugin it flips a preference that can never apply to anything. Config belongs
// with the thing that creates the feature.
//
// WHY A SECOND LINE, AND THE RULE THAT MUST NOT BE BROKEN. `Parse` above
// demands EXACTLY six fields behind the `nb1:` prefix, and every shipped
// NaviBeat does the same. Adding a seventh field, or bumping to `nb2:`, makes
// every INSTALLED client return "not ours": the playlist drops off the mixes
// shelf, loses its generated cover and its hour-of-day ordering, and its
// machine line becomes visible text in the description. So presentation gets
// its own line with its own version, which older clients simply do not
// recognise and skip.
//
//	NaviBeat Mixes: your morning, built from what you actually play at this time of day.
//	Made by NaviBeat  ·  navibeat.app
//	nb1:timeofday:morning:2026-07-28:affinity:30
//	nbui1:btn:sunrise:F2A65A:Morning Mix
//
// ORDERING. The presentation line goes LAST, after the descriptor, for the same
// reason the descriptor goes after the human text: `playlist.comment` is
// declared varchar(255), and if a future release enforces it, truncation must
// eat the most disposable thing first. Losing the button styling is a
// cosmetic loss; losing the descriptor would un-mix the playlist entirely.

// PresentationVersion opens the presentation line. Separate from Version so the
// two schemas can move independently.
const PresentationVersion = "nbui1"

const (
	styleTag  = "style"
	buttonTag = "btn"
)

// Style is how a client should render the mixes shelf.
type Style string

const (
	// StyleCover: NaviBeat's own generated artwork. The default, and what every
	// client already draws.
	StyleCover Style = "cover"
	// StyleButton: a compact row of buttons instead of artwork tiles.
	StyleButton Style = "button"
	// StyleMosaic: the server's own 2x2 album mosaic, i.e. NaviBeat draws
	// nothing special. This is what the app's old "NaviBeat covers" toggle
	// turned OFF, expressed on the wire so the app needs no toggle at all.
	StyleMosaic Style = "mosaic"
)

// ValidStyle reports whether s is a style this schema defines. Anything else
// must be ignored rather than guessed at.
func ValidStyle(s Style) bool {
	switch s {
	case StyleCover, StyleButton, StyleMosaic:
		return true
	}
	return false
}

// Button is the per-playlist button description.
type Button struct {
	// Glyph is a name from the NEUTRAL vocabulary below. Never an SF Symbol:
	// NaviBeat is two codebases, the Apple line and the Kotlin line for
	// Android, Linux and Windows, and both read this wire.
	Glyph string
	// Color is six hex digits, no leading '#'. Uppercase on the wire.
	Color string
	// Label is what the button says. May be empty, in which case the client
	// uses the playlist's own name. Written LAST so it may contain a colon.
	Label string
}

// Glyphs is the vocabulary a client is expected to understand. A client that
// meets an unknown name falls back to a generic icon rather than dropping the
// playlist, so this list may grow without a coordinated release.
var Glyphs = []string{
	"sunrise", "sun", "sunset", "moon",
	"sparkles", "compass", "heart", "star",
	"repeat", "radio", "shuffle", "clock",
	"gift", "waveform",
}

// FormatStyle renders the server-wide style line, which belongs on the CONTROL
// playlist.
//
// The control playlist is the right carrier and not an arbitrary choice:
// clients find it by machine line rather than by name (its name carries a
// user-configurable prefix), and they EXCLUDE it from the shelf, so no client
// ever renders its description. That means even a client too old to know this
// line can never show it to a person as stray text.
func FormatStyle(s Style) string {
	return strings.Join([]string{PresentationVersion, styleTag, string(s)}, ":")
}

// ParseStyle reads the style out of a control playlist description. ok=false
// means the description carries no instruction, which a client must treat as
// "keep doing what you were doing", NOT as StyleCover.
func ParseStyle(comment string) (Style, bool) {
	line, ok := machineLine(comment, styleTag)
	if !ok {
		return "", false
	}
	parts := strings.Split(line, ":")
	if len(parts) != 3 {
		return "", false
	}
	s := Style(strings.ToLower(parts[2]))
	if !ValidStyle(s) {
		return "", false
	}
	return s, true
}

// FormatButton renders one playlist's button line.
func FormatButton(b Button) string {
	return strings.Join([]string{
		PresentationVersion, buttonTag, b.Glyph, strings.ToUpper(b.Color), b.Label,
	}, ":")
}

// ParseButton reads a button line back.
//
// The label is the WHOLE remainder after the fourth field rather than a fifth
// field, because a colon is the delimiter and a human label may contain one
// ("Rock: the loud half"). A malformed colour rejects the entire line: unlike
// an unknown glyph, which degrades visibly to something generic, a half-read
// hex renders as an arbitrary shade with nothing at all to tell the user it
// was wrong.
func ParseButton(comment string) (Button, bool) {
	line, ok := machineLine(comment, buttonTag)
	if !ok {
		return Button{}, false
	}
	parts := strings.SplitN(line, ":", 5)
	if len(parts) < 4 {
		return Button{}, false
	}
	color := strings.ToUpper(parts[3])
	if !isSixHex(color) {
		return Button{}, false
	}
	label := ""
	if len(parts) == 5 {
		label = strings.TrimSpace(parts[4])
	}
	return Button{Glyph: strings.ToLower(parts[2]), Color: color, Label: label}, true
}

// AppendLine adds a line to a description, replacing any line that already
// carries the same presentation tag.
//
// Replacing rather than appending matters because descriptions are rewritten on
// every daily run: appending would grow the comment past the 255 budget within
// a week.
func AppendLine(comment, line string) string {
	tag := presentationTagOf(line)
	var kept []string
	for _, l := range strings.Split(comment, "\n") {
		if tag != "" && presentationTagOf(strings.TrimSpace(l)) == tag {
			continue
		}
		kept = append(kept, l)
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	return strings.Join(append(kept, line), "\n")
}

func presentationTagOf(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, PresentationVersion+":") {
		return ""
	}
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func machineLine(comment, tag string) (string, bool) {
	prefix := PresentationVersion + ":" + tag + ":"
	for _, line := range strings.Split(comment, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return line, true
		}
	}
	return "", false
}

func isSixHex(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
