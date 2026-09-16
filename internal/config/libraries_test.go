package config

import "testing"

// #502335 (James, 2026-09-13): a LoFi library skewed every mix. The pool can
// be told which libraries to draw from; empty keeps every existing install
// exactly as it was.
func TestLibrariesDefaultsToEveryLibrary(t *testing.T) {
	c := Load(getterFrom(nil))
	if len(c.Libraries) != 0 {
		t.Fatalf("no setting means no restriction, got %v", c.Libraries)
	}
}

func TestLibrariesIsACommaSeparatedListOfIds(t *testing.T) {
	c := Load(getterFrom(map[string]string{"libraries": " 1, 3 ,,7 "}))
	want := []string{"1", "3", "7"}
	if len(c.Libraries) != len(want) {
		t.Fatalf("got %v, want %v", c.Libraries, want)
	}
	for i := range want {
		if c.Libraries[i] != want[i] {
			t.Fatalf("got %v, want %v", c.Libraries, want)
		}
	}
}
