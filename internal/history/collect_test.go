package history

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

// Navidrome hands the history over one page at a time and says when there is
// no next page. Every page is kept, in order.
func TestCollectReadsEveryPageUntilTheServerSaysStop(t *testing.T) {
	at := time.Unix(1_760_000_000, 0)
	pages := [][]mixes.Play{
		{{TrackID: "a", At: at}, {TrackID: "b", At: at}},
		{{TrackID: "c", At: at}},
	}
	calls := 0
	got, err := Collect(func() ([]mixes.Play, bool, error) {
		p := pages[calls]
		calls++
		return p, calls < len(pages), nil
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("fetched %d pages, want 2", calls)
	}
	want := []mixes.Play{{TrackID: "a", At: at}, {TrackID: "b", At: at}, {TrackID: "c", At: at}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// Any error is the caller's cue to fall back; a half read history must not
// be ranked as if it were whole.
func TestCollectReturnsTheErrorAndNoPlays(t *testing.T) {
	calls := 0
	got, err := Collect(func() ([]mixes.Play, bool, error) {
		calls++
		if calls == 2 {
			return nil, false, errors.New(`"scrobbleretriever_getscrobbles" failed`)
		}
		return []mixes.Play{{TrackID: "a"}}, true, nil
	}, 10)
	if err == nil || got != nil {
		t.Fatalf("got %v, %v; want nil plays and the error", got, err)
	}
}

// A server that never stops saying "more" must not hold the run forever.
func TestCollectStopsAtThePageLimit(t *testing.T) {
	calls := 0
	_, err := Collect(func() ([]mixes.Play, bool, error) {
		calls++
		return []mixes.Play{{TrackID: "a"}}, true, nil
	}, 3)
	if err == nil {
		t.Fatal("an endless history was accepted")
	}
	if calls != 3 {
		t.Fatalf("fetched %d pages, want the limit of 3", calls)
	}
}
