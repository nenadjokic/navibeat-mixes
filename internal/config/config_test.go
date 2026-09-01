package config

import "testing"

func getterFrom(m map[string]string) Getter {
	return func(k string) string { return m[k] }
}

// Everything must work on a server nobody configured, or most people never
// see a working plugin at all.
func TestDefaultsProduceAWorkingPluginWithNoConfiguration(t *testing.T) {
	c := Load(getterFrom(nil))
	if c.MixSize != 30 || c.RediscoverMonths != 6 || c.MinEventsForAffinity != 150 {
		t.Errorf("unexpected defaults: %+v", c)
	}
	for _, slot := range []string{"morning", "afternoon", "evening", "night", "rediscover", "wrapped"} {
		if !c.MixEnabled(slot) {
			t.Errorf("%s is disabled by default", slot)
		}
	}
	if c.WrappedSharing {
		t.Error("sharing defaults to ON. It must default to off.")
	}
}

func TestPerMixSwitchesTurnOneMixOffWithoutTouchingTheRest(t *testing.T) {
	c := Load(getterFrom(map[string]string{"enableNight": "false"}))
	if c.MixEnabled("night") {
		t.Error("night stayed enabled after being switched off")
	}
	if !c.MixEnabled("morning") {
		t.Error("switching night off also disabled morning")
	}
}

// A typo must never be the thing that starts sending data.
func TestSharingNeedsAnExplicitTrue(t *testing.T) {
	for _, v := range []string{"", "yes", "1", "on", "TRUE "} {
		c := Load(getterFrom(map[string]string{"wrappedSharing": v}))
		want := v == "TRUE "
		if c.WrappedSharing != want {
			t.Errorf("wrappedSharing=%q gave %v, want %v", v, c.WrappedSharing, want)
		}
	}
}

// A broken value in one field must not take the whole plugin down with it.
func TestNonsenseValuesFallBackToDefaults(t *testing.T) {
	c := Load(getterFrom(map[string]string{
		"mixSize": "banana", "rediscoverMonths": "-4", "genreNoiseThreshold": "17",
	}))
	if c.MixSize != 30 || c.RediscoverMonths != 6 || c.GenreNoiseThreshold != 0.6 {
		t.Errorf("bad values were accepted: %+v", c)
	}
}

func TestPrefixGainsATrailingSpace(t *testing.T) {
	if got := Load(getterFrom(map[string]string{"prefix": "**"})).PlaylistName("Morning"); got != "** Morning" {
		t.Errorf("PlaylistName = %q, want %q", got, "** Morning")
	}
}

// The older comma-separated key must keep working, or an upgrade silently
// changes what an existing server generates.
func TestTheOlderEnabledMixesKeyStillWorks(t *testing.T) {
	c := Load(getterFrom(map[string]string{"enabledMixes": "morning,rediscover"}))
	if !c.MixEnabled("morning") || c.MixEnabled("night") {
		t.Errorf("enabledMixes was ignored: %+v", c.EnabledMixes)
	}
}

// Sly777, issue #1: the per-artist cap used to be a constant in main.go, so
// "I would like to have 1 song per musician" had no answer short of a rebuild.
func TestMaxPerArtistDefaultsToThreeAndIsConfigurable(t *testing.T) {
	if Defaults().MaxPerArtist != 3 {
		t.Fatalf("default changed, every existing install would shift: %d", Defaults().MaxPerArtist)
	}
	c := Load(func(k string) string {
		if k == "maxPerArtist" {
			return "1"
		}
		return ""
	})
	if c.MaxPerArtist != 1 {
		t.Fatalf("setting ignored, got %d", c.MaxPerArtist)
	}
}

// The setting joshp23's issue #3 produced: which accounts get mixes at all.
//
// The first case is the one that matters most, because it is every install that
// already exists.
func TestBuildsForEmptyMeansEveryone(t *testing.T) {
	c := Defaults()
	for _, name := range []string{"nenad", "josh", "someone-else"} {
		if !c.BuildsFor(name) {
			t.Fatalf("an unset list must build for %q", name)
		}
	}
}

func TestBuildsForOnlyTheListedAccounts(t *testing.T) {
	c := Load(func(key string) string {
		if key == "onlyForUsers" {
			return "josh, nenad"
		}
		return ""
	})
	if !c.BuildsFor("josh") || !c.BuildsFor("nenad") {
		t.Fatal("a listed account was skipped")
	}
	if c.BuildsFor("guest") {
		t.Fatal("an account outside the list was built for")
	}
}

// A typo in case would otherwise produce a server where the plugin silently
// does nothing, with nothing to read anywhere that says why.
func TestBuildsForIgnoresCaseAndSpacing(t *testing.T) {
	c := Load(func(key string) string {
		if key == "onlyForUsers" {
			return "  Josh ,, NENAD  "
		}
		return ""
	})
	if !c.BuildsFor("josh") {
		t.Fatal("Josh should match josh")
	}
	if !c.BuildsFor("nenad") {
		t.Fatal("NENAD should match nenad")
	}
	if len(c.OnlyForUsers) != 2 {
		t.Fatalf("empty entries should be dropped, got %v", c.OnlyForUsers)
	}
}

// Whitespace only is not a list, it is an empty field with a stray space in it,
// and reading it as a list would stop the plugin for everybody.
func TestBuildsForTreatsBlankAsUnset(t *testing.T) {
	c := Load(func(key string) string {
		if key == "onlyForUsers" {
			return "   "
		}
		return ""
	})
	if !c.BuildsFor("anyone") {
		t.Fatal("a blank field must mean everyone, not nobody")
	}
}
