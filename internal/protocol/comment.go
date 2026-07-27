// Package protocol formats and parses the playlist description that NaviBeat
// Mixes writes into `playlist.comment`.
//
// A Navidrome plugin has no inbound channel: it cannot serve HTTP, register a
// route, or draw any UI. The only thing it can put in front of a client is a
// playlist, so the description doubles as the wire format. Most clients render
// `comment` as the playlist description, which forces two constraints:
//
//   - It has to read as ordinary human text, because most people looking at it
//     are not using NaviBeat and never will be. One short extra line is the
//     most a foreign client should ever have to show.
//   - The machine part must be recognisable without a handshake or a version
//     negotiation, since there is nothing to handshake with.
//
// Layout is therefore a human sentence, a newline, and exactly one machine
// line. The order matters: `playlist.comment` is declared varchar(255) and
// merely happens not to be enforced by SQLite today. Putting the human text
// first means that if a future release does enforce the limit, truncation
// removes the machine tail and leaves a readable description behind, rather
// than cutting the sentence and leaving a dangling fragment.
package protocol

import (
	"strconv"
	"strings"
)

// Version is the schema tag that opens the machine line. A client that does
// not recognise it must ignore the line rather than guess, which is why the
// version is first and not last.
const Version = "nb1"

// Meta is the machine-readable half of a description.
type Meta struct {
	// Kind is the family of mix, for example "timeofday" or "rediscover".
	Kind string
	// Slot identifies which mix within the kind, for example "morning".
	Slot string
	// Date is the generation date, YYYY-MM-DD in server local time.
	Date string
	// Mode records how the mix was built, "affinity" or "fallback", so the
	// behaviour is never mysterious to someone reading the description.
	Mode string
	// Count is how many tracks were selected.
	Count int
}

const fieldCount = 6

// Attribution is the one line of branding these playlists carry.
//
// It sits in the DESCRIPTION and never in the playlist name, which is the
// whole trade-off: a name like "NaviBeat Morning" reads as an advert and gets
// the plugin uninstalled, while a name of "Morning" that says nothing at all
// means a user can enjoy these mixes for a year without ever learning where
// they came from. One quiet line under the sentence that explains the mix is
// the version a person does not resent, and it is visible in every client on
// every playlist rather than once on an install screen nobody revisits.
const Attribution = "Made by NaviBeat  ·  navibeat.app"

// Format renders a description: the sentence a person reads, the attribution,
// then the machine line last.
//
// Order is deliberate and load bearing. `playlist.comment` is declared
// varchar(255) and merely happens not to be enforced today; if that ever
// changes, truncation eats from the end, so it takes the machine tail first
// and the human text last.
func Format(human string, m Meta) string {
	machine := strings.Join([]string{
		Version, m.Kind, m.Slot, m.Date, m.Mode, strconv.Itoa(m.Count),
	}, ":")
	return human + "\n" + Attribution + "\n" + machine
}

// Parse extracts the machine line from a description. It returns ok=false for
// anything that is not unambiguously ours: a foreign playlist, a truncated
// tail, or a schema version this build does not understand. Callers must treat
// ok=false as "leave this playlist alone", which is what keeps the plugin from
// ever modifying a playlist a user made by hand.
func Parse(comment string) (Meta, bool) {
	for _, line := range strings.Split(comment, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, Version+":") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != fieldCount {
			// Truncated, or padded with something we did not write. Either
			// way it is not safe to act on.
			return Meta{}, false
		}
		count, err := strconv.Atoi(parts[5])
		if err != nil {
			return Meta{}, false
		}
		return Meta{
			Kind:  parts[1],
			Slot:  parts[2],
			Date:  parts[3],
			Mode:  parts[4],
			Count: count,
		}, true
	}
	return Meta{}, false
}
