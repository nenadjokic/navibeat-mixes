package resume

import (
	"errors"
	"testing"
)

// fakeHost behaves the way Navidrome 0.63.2's scheduler does, read from
// plugins/host_scheduler.go: a one-time id is refused while it is in the map,
// and the map entry is removed only AFTER its callback has returned. Fire
// runs a pending schedule's callback with exactly that ordering, so a test
// can schedule from inside a running continuation the way the plugin does.
type fakeHost struct {
	schedules map[string]string // id -> payload
	kv        map[string][]byte
	cancelled []string
}

func newFakeHost() *fakeHost {
	return &fakeHost{schedules: map[string]string{}, kv: map[string][]byte{}}
}

func (f *fakeHost) ScheduleOneTime(delay int32, payload, id string) (string, error) {
	if _, exists := f.schedules[id]; exists {
		return "", errors.New("schedule ID " + id + " already exists")
	}
	f.schedules[id] = payload
	return id, nil
}

func (f *fakeHost) CancelSchedule(id string) error {
	if _, exists := f.schedules[id]; !exists {
		return errors.New("schedule ID " + id + " not found")
	}
	delete(f.schedules, id)
	f.cancelled = append(f.cancelled, id)
	return nil
}

// Fire runs the callback for a pending id: the entry stays registered while
// fn runs and is deleted afterwards, as the host's timer func does.
func (f *fakeHost) Fire(id string, fn func()) {
	if _, ok := f.schedules[id]; !ok {
		return
	}
	fn()
	delete(f.schedules, id)
}

func (f *fakeHost) Get(key string) ([]byte, bool, error) {
	v, ok := f.kv[key]
	return v, ok, nil
}
func (f *fakeHost) Set(key string, value []byte) error { f.kv[key] = value; return nil }
func (f *fakeHost) Delete(key string) error            { delete(f.kv, key); return nil }

// THE 2026-08-31 CHAIN BREAK. A continuation that needs another continuation
// scheduled it under its own id while it was still registered, and the host
// refused. Three calls in a row must all get their successor.
func TestContinueChainsFromInsideARunningContinuation(t *testing.T) {
	h := newFakeHost()

	first, err := Continue(h, h, 5, "generate")
	if err != nil {
		t.Fatalf("first continuation: %v", err)
	}
	if first != ContinuePrefix+"-1" {
		t.Fatalf("first id = %q", first)
	}

	var second, third string
	h.Fire(first, func() {
		second, err = Continue(h, h, 5, "generate")
		if err != nil {
			t.Fatalf("scheduling from inside the first continuation: %v", err)
		}
	})
	if second == first {
		t.Fatal("the successor must not reuse the running id, the host's cleanup would drop it")
	}
	if _, pending := h.schedules[second]; !pending {
		t.Fatalf("%s did not survive the host's cleanup of %s", second, first)
	}

	h.Fire(second, func() {
		third, err = Continue(h, h, 5, "generate")
		if err != nil {
			t.Fatalf("scheduling from inside the second continuation: %v", err)
		}
	})
	if third != ContinuePrefix+"-3" {
		t.Fatalf("third id = %q, want a numbered successor", third)
	}
	if _, pending := h.schedules[third]; !pending {
		t.Fatal("the third continuation is not pending")
	}
	if len(h.schedules) != 1 {
		t.Fatalf("host holds %d schedules, want only the pending one", len(h.schedules))
	}
}

// A continuation asked for while another is still pending (a re-roll from the
// control playlist during a nightly chain) supersedes it, so two callbacks
// never work the same ledger at once.
func TestContinueCancelsAPendingPredecessor(t *testing.T) {
	h := newFakeHost()
	first, _ := Continue(h, h, 5, "generate")
	second, err := Continue(h, h, 5, "generate")
	if err != nil {
		t.Fatalf("second continuation: %v", err)
	}
	if _, still := h.schedules[first]; still {
		t.Fatalf("%s is still pending next to %s", first, second)
	}
	if len(h.cancelled) != 1 || h.cancelled[0] != first {
		t.Fatalf("cancelled = %v, want [%s]", h.cancelled, first)
	}
	if got := string(h.kv[ContinueKey]); got != second {
		t.Fatalf("store remembers %q, want %q", got, second)
	}
}

// On load the host has forgotten every schedule, so the remembered id is
// stale: cancel it (the host answers not found, which is fine), forget it, and
// let the next Continue start a clean chain.
func TestCancelStaleForgetsWhatTheHostAlreadyDropped(t *testing.T) {
	h := newFakeHost()
	h.kv[ContinueKey] = []byte(ContinuePrefix + "-7")

	if got := CancelStale(h, h); got != ContinuePrefix+"-7" {
		t.Fatalf("CancelStale = %q", got)
	}
	if _, still := h.kv[ContinueKey]; still {
		t.Fatal("the stale id was not forgotten")
	}
	if got := CancelStale(h, h); got != "" {
		t.Fatalf("a second CancelStale found %q", got)
	}
	next, err := Continue(h, h, 5, "generate")
	if err != nil || next != ContinuePrefix+"-1" {
		t.Fatalf("after a clean start Continue = %q, %v", next, err)
	}
}

// A corrupt or foreign value under the key must not stop the chain.
func TestContinueIgnoresGarbageInTheStore(t *testing.T) {
	h := newFakeHost()
	h.kv[ContinueKey] = []byte("navibeat-mixes-continue")
	next, err := Continue(h, h, 5, "generate")
	if err != nil || next != ContinuePrefix+"-1" {
		t.Fatalf("Continue = %q, %v", next, err)
	}
	if len(h.cancelled) != 0 {
		t.Fatalf("cancelled %v for a value that names no continuation", h.cancelled)
	}
}

// When the host refuses, the caller hears about it and the store keeps
// pointing at whatever was pending before.
func TestContinueReportsAHostRefusal(t *testing.T) {
	h := newFakeHost()
	h.schedules[ContinuePrefix+"-1"] = "generate" // registered by someone the store does not know about
	if _, err := Continue(h, h, 5, "generate"); err == nil {
		t.Fatal("a refused schedule must be an error")
	}
	if _, ok := h.kv[ContinueKey]; ok {
		t.Fatal("a refused schedule must not be remembered as pending")
	}
}
