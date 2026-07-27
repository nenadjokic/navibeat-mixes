// Package wrapped builds the aggregate that Wrapped sharing sends, and nothing
// else. It is a separate package so that exactly what leaves the machine is
// one short file anybody can read before deciding to trust it.
//
// SHARING IS OFF UNLESS THE USER TURNS IT ON. A self-hosting audience is the
// least forgiving possible audience for silent telemetry, and rightly so, so
// the rules here are strict and the payload is deliberately boring:
//
//   - counts and names only, never a play log and never track ids
//   - nothing identifying the server: no hostname, no url, no user id, no
//     library size, no version string, no timing information
//   - no username, since that is often a real name
//
// If a reviewer reads one file in this repository before installing it, this
// should be that file, and it should be short enough to finish.
package wrapped

import (
	"encoding/json"
	"sort"
)

// TopArtist is one line of the recap.
type TopArtist struct {
	Name  string `json:"name"`
	Plays int    `json:"plays"`
}

// Payload is the ENTIRE body sent to the sharing endpoint. If a field is not
// on this struct, it does not leave the machine.
type Payload struct {
	// Year the recap covers.
	Year int `json:"year"`
	// TotalPlays observed by the plugin.
	TotalPlays int `json:"totalPlays"`
	// DistinctTracks played at least once.
	DistinctTracks int `json:"distinctTracks"`
	// TopArtists, names and counts.
	TopArtists []TopArtist `json:"topArtists"`
	// PeakHour is the hour of day with the most plays, 0 to 23.
	PeakHour int `json:"peakHour"`
}

// maxArtists bounds the payload. A recap is a highlight, and a longer list
// starts to describe a person's library rather than summarise it.
const maxArtists = 10

// Build assembles the payload from aggregate inputs.
//
// It takes counts, never events. There is no code path here that could send a
// play log, because it is never handed one.
func Build(year int, hours [24]int, artistPlays map[string]int, distinctTracks int) Payload {
	total := 0
	peak, peakN := 0, -1
	for h, n := range hours {
		total += n
		if n > peakN {
			peak, peakN = h, n
		}
	}

	artists := make([]TopArtist, 0, len(artistPlays))
	for name, n := range artistPlays {
		if name == "" || n <= 0 {
			continue
		}
		artists = append(artists, TopArtist{Name: name, Plays: n})
	}
	sort.Slice(artists, func(i, j int) bool {
		if artists[i].Plays != artists[j].Plays {
			return artists[i].Plays > artists[j].Plays
		}
		return artists[i].Name < artists[j].Name
	})
	if len(artists) > maxArtists {
		artists = artists[:maxArtists]
	}

	return Payload{
		Year:           year,
		TotalPlays:     total,
		DistinctTracks: distinctTracks,
		TopArtists:     artists,
		PeakHour:       peak,
	}
}

// Encode renders the payload for transport.
func (p Payload) Encode() ([]byte, error) { return json.Marshal(p) }

// Endpoint is where a recap is sent. It is the only host the manifest permits
// the plugin to reach.
const Endpoint = "https://navibeat.app/api/wrapped"
