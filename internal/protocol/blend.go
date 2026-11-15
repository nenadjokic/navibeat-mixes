package protocol

import (
	"strconv"
	"strings"
)

// Blend (NaviBeat #503551): one playlist per configured PAIR of server users,
// built from both listening histories. The `nb1:` descriptor identifies it as
// a mix like any other (kind "blend"), and this second line tells NaviBeat who
// the two people are and how close their taste is, so the app can draw both
// avatars and a match chip.
//
//	NaviBeat Mixes: a Blend of what you both play, shared songs first.
//	Made by NaviBeat  ·  navibeat.app
//	nb1:blend:blend-1f0c3a9e:2026-11-16:affinity:30
//	nbblend1:72:214:ana:nenad
//
// Fields after the tag, exactly four:
//
//	match   integer 0..100, the share of the lighter listener's played songs
//	        the other one has played too
//	shared  integer >= 0, how many songs both of them have played
//	userA   Subsonic username (the user id the Subsonic API knows), escaped
//	userB   the other username, escaped
//
// The two usernames are written in case-insensitive sorted order, so both
// copies of one Blend (each user gets their own) carry the same line.
//
// ESCAPING. A Navidrome username is free text, so `%`, `:`, CR and LF are
// written as %25, %3A, %0D and %0A. Nothing else is escaped, which keeps the
// line short for the 255 budget and readable in a raw description.
//
// WHY ITS OWN LINE. The `nb1:` line must stay exactly six fields (see
// presentation.go); an installed NaviBeat strips any `nb<alnum>:` line it does
// not understand, so an older app shows the Blend as an ordinary mix and never
// shows this line as text. The tag has no hyphen or underscore on purpose: the
// app's strip rule only matches letters and digits after `nb`.
const BlendVersion = "nbblend1"

// Blend is the parsed blend line.
type Blend struct {
	Match  int
	Shared int
	UserA  string
	UserB  string
}

var blendEscaper = strings.NewReplacer("%", "%25", ":", "%3A", "\r", "%0D", "\n", "%0A")
var blendUnescaper = strings.NewReplacer("%3A", ":", "%3a", ":", "%0D", "\r", "%0d", "\r", "%0A", "\n", "%0a", "\n", "%25", "%")

// BlendPair returns the two usernames in the order the line carries them.
func BlendPair(a, b string) (string, string) {
	if strings.ToLower(b) < strings.ToLower(a) {
		return b, a
	}
	return a, b
}

// FormatBlend renders the blend line. The match is clamped to 0..100 and a
// negative shared count to 0, so a bug upstream can never write a line the
// app rejects.
func FormatBlend(b Blend) string {
	match := b.Match
	if match < 0 {
		match = 0
	}
	if match > 100 {
		match = 100
	}
	shared := b.Shared
	if shared < 0 {
		shared = 0
	}
	a, c := BlendPair(b.UserA, b.UserB)
	return strings.Join([]string{
		BlendVersion, strconv.Itoa(match), strconv.Itoa(shared),
		blendEscaper.Replace(a), blendEscaper.Replace(c),
	}, ":")
}

// ParseBlend finds the blend line in a description. ok=false for a
// description without one, or with one that does not have exactly the
// documented shape.
func ParseBlend(comment string) (Blend, bool) {
	for _, line := range strings.Split(comment, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, BlendVersion+":") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 5 {
			return Blend{}, false
		}
		match, err1 := strconv.Atoi(parts[1])
		shared, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || match < 0 || match > 100 || shared < 0 {
			return Blend{}, false
		}
		a := blendUnescaper.Replace(parts[3])
		b := blendUnescaper.Replace(parts[4])
		if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
			return Blend{}, false
		}
		return Blend{Match: match, Shared: shared, UserA: a, UserB: b}, true
	}
	return Blend{}, false
}
