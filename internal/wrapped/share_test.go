package wrapped

import (
	"encoding/json"
	"strings"
	"testing"
)

// This is the test that matters most in the whole repository. Sharing is the
// only thing in the plugin that can send anything anywhere, and a self-hosting
// audience is right to assume the worst until shown otherwise. If a field ever
// appears in the payload that is not on this list, that is a breach of what
// the config text promises, and it should fail the build rather than ship.
func TestPayloadContainsNothingBeyondAggregates(t *testing.T) {
	p := Build(2026, [24]int{8: 5, 21: 12}, map[string]int{"Artist": 3}, 7)
	data, err := p.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("payload is not an object: %v", err)
	}

	allowed := map[string]bool{
		"year": true, "totalPlays": true, "distinctTracks": true,
		"topArtists": true, "peakHour": true,
	}
	for k := range got {
		if !allowed[k] {
			t.Errorf("payload carries an unexpected field %q. Anything not on the allow list is data the user was never told would leave their machine", k)
		}
	}

	// Nothing that could identify a server or a person.
	body := strings.ToLower(string(data))
	for _, forbidden := range []string{"host", "url", "server", "userid", "username", "trackid", "path", "library", "version", "ip"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("payload mentions %q: %s", forbidden, data)
		}
	}
}

func TestBuildRanksArtistsAndBoundsTheList(t *testing.T) {
	plays := map[string]int{}
	for i := 0; i < 50; i++ {
		plays[string(rune('a'+i%26))+string(rune('0'+i/26))] = i
	}
	p := Build(2026, [24]int{}, plays, 50)
	if len(p.TopArtists) != maxArtists {
		t.Errorf("sent %d artists, want at most %d: a longer list describes a library rather than summarising it", len(p.TopArtists), maxArtists)
	}
	for i := 1; i < len(p.TopArtists); i++ {
		if p.TopArtists[i].Plays > p.TopArtists[i-1].Plays {
			t.Error("artists are not ranked by plays")
		}
	}
}

func TestBuildFindsThePeakHour(t *testing.T) {
	var hours [24]int
	hours[7] = 3
	hours[22] = 19
	p := Build(2026, hours, nil, 0)
	if p.PeakHour != 22 {
		t.Errorf("PeakHour = %d, want 22", p.PeakHour)
	}
	if p.TotalPlays != 22 {
		t.Errorf("TotalPlays = %d, want 22", p.TotalPlays)
	}
}

func TestBuildSkipsEmptyAndZeroArtists(t *testing.T) {
	p := Build(2026, [24]int{}, map[string]int{"": 9, "Real": 2, "Zero": 0}, 1)
	if len(p.TopArtists) != 1 || p.TopArtists[0].Name != "Real" {
		t.Errorf("TopArtists = %+v, want only Real", p.TopArtists)
	}
}
