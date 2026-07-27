package collector

import (
	"testing"
	"time"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

func at(hour, min, sec int) time.Time {
	return time.Date(2026, 7, 27, hour, min, sec, 0, time.UTC)
}

func TestAcceptRecordsHourWeekdayAndSlot(t *testing.T) {
	s := NewState()
	if !s.Accept(Event{TrackID: "t1", At: at(8, 0, 0), Duration: 3 * time.Minute}) {
		t.Fatal("first play was rejected")
	}
	if s.Hours[8] != 1 {
		t.Errorf("Hours[8] = %d, want 1", s.Hours[8])
	}
	if s.Events != 1 {
		t.Errorf("Events = %d, want 1", s.Events)
	}
	if got := s.Tracks["t1"][string(mixes.Morning)]; got != 1 {
		t.Errorf("morning count = %d, want 1", got)
	}
}

// The case this rule was written for: a real server showed roughly a quarter
// of all scrobbles were duplicates, one track arriving 46 times in a single
// second. One stuck client must not get to decide what the user's morning
// sounds like.
func TestAcceptIgnoresARepeatBurstOfTheSameTrack(t *testing.T) {
	s := NewState()
	s.Accept(Event{TrackID: "stuck", At: at(9, 0, 0), Duration: 4 * time.Minute})
	rejected := 0
	for i := 1; i < 46; i++ {
		if !s.Accept(Event{TrackID: "stuck", At: at(9, 0, 0), Duration: 4 * time.Minute}) {
			rejected++
		}
	}
	if rejected != 45 {
		t.Errorf("rejected %d of 45 duplicates", rejected)
	}
	if s.Events != 1 {
		t.Errorf("Events = %d, want 1: the burst was one play", s.Events)
	}
}

// A genuine repeat listen, after the track has had time to finish, is a real
// second play and must count. Otherwise someone playing a song twice in a
// morning is recorded as playing it once.
func TestAcceptCountsAGenuineRepeatAfterTheWindow(t *testing.T) {
	s := NewState()
	s.Accept(Event{TrackID: "loved", At: at(9, 0, 0), Duration: 3 * time.Minute})
	if !s.Accept(Event{TrackID: "loved", At: at(9, 4, 0), Duration: 3 * time.Minute}) {
		t.Fatal("a repeat four minutes later was rejected, but the track is three minutes long")
	}
	if s.Events != 2 {
		t.Errorf("Events = %d, want 2", s.Events)
	}
}

// Short tracks would otherwise get a dedup window small enough for a burst to
// slip through it.
func TestDedupWindowHasAFloorForVeryShortTracks(t *testing.T) {
	s := NewState()
	s.Accept(Event{TrackID: "interlude", At: at(9, 0, 0), Duration: 5 * time.Second})
	if s.Accept(Event{TrackID: "interlude", At: at(9, 0, 20), Duration: 5 * time.Second}) {
		t.Error("a repeat 20s later counted, but the floor is 60s")
	}
}

func TestDifferentTracksNeverDeduplicateAgainstEachOther(t *testing.T) {
	s := NewState()
	s.Accept(Event{TrackID: "a", At: at(9, 0, 0), Duration: time.Minute})
	if !s.Accept(Event{TrackID: "b", At: at(9, 0, 1), Duration: time.Minute}) {
		t.Error("a different track one second later was rejected")
	}
}

func TestAcceptRejectsUnusableEvents(t *testing.T) {
	s := NewState()
	if s.Accept(Event{TrackID: "", At: at(9, 0, 0)}) {
		t.Error("accepted an event with no track id")
	}
	if s.Accept(Event{TrackID: "t"}) {
		t.Error("accepted an event with no timestamp")
	}
}

func TestRoundTripThroughStorage(t *testing.T) {
	s := NewState()
	s.Accept(Event{TrackID: "t1", At: at(7, 0, 0), Duration: time.Minute})
	data, err := s.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back := Unmarshal(data)
	if back.Events != s.Events || back.Hours[7] != 1 {
		t.Errorf("state did not survive the round trip: %+v", back)
	}
}

// Losing a histogram costs a warm-up period. Failing hard would stop the
// plugin producing anything, which is far worse.
func TestUnmarshalSurvivesRubbish(t *testing.T) {
	for _, in := range [][]byte{nil, []byte(""), []byte("not json"), []byte("{")} {
		s := Unmarshal(in)
		if s == nil || s.Tracks == nil || s.LastSeen == nil {
			t.Errorf("Unmarshal(%q) returned unusable state", in)
		}
	}
}

func TestPruneKeepsTheMostPlayedAndBoundsGrowth(t *testing.T) {
	s := NewState()
	for i := 0; i < 100; i++ {
		id := "t" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		for n := 0; n <= i; n++ {
			s.Tracks[id] = map[string]int{"morning": i + 1}
		}
	}
	s.Prune(10)
	if len(s.Tracks) != 10 {
		t.Fatalf("kept %d tracks, want 10", len(s.Tracks))
	}
}

func TestPruneIsANoOpBelowTheCap(t *testing.T) {
	s := NewState()
	s.Accept(Event{TrackID: "only", At: at(9, 0, 0), Duration: time.Minute})
	s.Prune(5000)
	if len(s.Tracks) != 1 {
		t.Errorf("pruning below the cap changed the state")
	}
}

func TestAffinityProjectsIntoTheSelectionShape(t *testing.T) {
	s := NewState()
	s.Accept(Event{TrackID: "t1", At: at(8, 0, 0), Duration: time.Minute})
	aff := s.Affinity()
	if aff["t1"][mixes.Morning] != 1 {
		t.Errorf("affinity = %+v, want morning=1", aff["t1"])
	}
}
