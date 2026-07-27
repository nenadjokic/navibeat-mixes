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
	if got := Load(getterFrom(map[string]string{"prefix": "**"})).PlaylistName("morning"); got != "** Morning" {
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
