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

// FunkeCoder23, issue #5: in Navidrome's plugin settings the Name fields were
// empty, typing into them did not stick, and no icon or colour was ever
// applied. The cause was the DOT in the config keys ("name.morning"), which
// JsonForms treats as a path separator, so the control read and wrote a nested
// object while this plugin only ever looked for the flat dotted key.
//
// The keys are dot free from 0.9.7 on. These four tests pin all three shapes a
// value can arrive in, because two of them belong to servers configured before
// the rename and must not lose what their owner typed.
func TestNamesIconsAndColoursReadFromTheDotFreeKeys(t *testing.T) {
	c := Load(getterFrom(map[string]string{
		"nameMorning":     "Jutro",
		"nameNight":       "Noc",
		"iconRediscover":  "compass",
		"colorRediscover": "#f00",
		"iconNewMusic":    "star",
		"colorDailyMix":   "112233",
	}))
	if c.SlotNames["morning"] != "Jutro" || c.SlotNames["night"] != "Noc" {
		t.Errorf("names not read: %v", c.SlotNames)
	}
	if c.SlotNames["evening"] != "Evening" {
		t.Errorf("an untouched slot lost its default: %q", c.SlotNames["evening"])
	}
	icon, color := c.ButtonFor("rediscover")
	if icon != "compass" || color != "FF0000" {
		t.Errorf("rediscover button = %q/%q, want compass/FF0000", icon, color)
	}
	if icon, _ := c.ButtonFor("newmusic"); icon != "star" {
		t.Errorf("newmusic icon = %q, want star", icon)
	}
	if _, color := c.ButtonFor("dailymix"); color != "112233" {
		t.Errorf("dailymix colour = %q, want 112233", color)
	}
}

// Shape two: the old dotted key, which is what a server configured through the
// API by hand still carries.
func TestTheOldDottedKeysAreStillRead(t *testing.T) {
	c := Load(getterFrom(map[string]string{
		"name.morning":    "Jutro",
		"icon.loved":      "sparkles",
		"color.loved":     "0A0B0C",
		"name.rediscover": "Ponovo",
	}))
	if c.SlotNames["morning"] != "Jutro" || c.SlotNames["rediscover"] != "Ponovo" {
		t.Errorf("dotted names were dropped: %v", c.SlotNames)
	}
	icon, color := c.ButtonFor("loved")
	if icon != "sparkles" || color != "0A0B0C" {
		t.Errorf("loved button = %q/%q, want sparkles/0A0B0C", icon, color)
	}
}

// Shape three: what Navidrome's settings page actually wrote through the dotted
// key. JsonForms saved {"name":{"morning":"X"}}, and parsePluginConfig serializes
// any nested value as JSON under its top-level key, so the plugin is handed
// config["name"] = "{\"morning\":\"X\"}".
func TestTheNestedBlobTheConfigUIWroteIsStillRead(t *testing.T) {
	c := Load(getterFrom(map[string]string{
		"name":  `{"morning":"Jutro","night":"Noc"}`,
		"icon":  `{"onrepeat":"repeat","genreradio":"radio"}`,
		"color": `{"onrepeat":"#ABC"}`,
	}))
	if c.SlotNames["morning"] != "Jutro" || c.SlotNames["night"] != "Noc" {
		t.Errorf("nested names were dropped: %v", c.SlotNames)
	}
	if c.SlotNames["afternoon"] != "Afternoon" {
		t.Errorf("a slot missing from the blob lost its default: %q", c.SlotNames["afternoon"])
	}
	icon, color := c.ButtonFor("onrepeat")
	if icon != "repeat" || color != "AABBCC" {
		t.Errorf("onrepeat button = %q/%q, want repeat/AABBCC", icon, color)
	}
	if icon, _ := c.ButtonFor("genreradio"); icon != "radio" {
		t.Errorf("genreradio icon = %q, want radio", icon)
	}
}

// The order is what protects an upgrade: someone who edits the settings page
// after upgrading writes the dot free key, and that new value must win over
// whatever the two older shapes still hold.
func TestTheDotFreeKeyWinsOverBothOlderShapes(t *testing.T) {
	c := Load(getterFrom(map[string]string{
		"nameMorning":  "New",
		"name.morning": "Dotted",
		"name":         `{"morning":"Nested"}`,
		"iconLoved":    "star",
		"icon.loved":   "heart",
		"icon":         `{"loved":"compass"}`,
	}))
	if c.SlotNames["morning"] != "New" {
		t.Errorf("SlotNames[morning] = %q, want New", c.SlotNames["morning"])
	}
	if icon, _ := c.ButtonFor("loved"); icon != "star" {
		t.Errorf("loved icon = %q, want star", icon)
	}
	// And the dotted key must still beat the nested blob when the dot free key
	// is absent. Measured against the real 0.9.6 manifest, Navidrome's Ajv
	// (useDefaults: true) wrote the literal dotted key into the form data
	// before any control rendered, so on a stock server the flat dotted key
	// holds what the owner typed and a nested blob is the older of the two.
	c = Load(getterFrom(map[string]string{
		"name.morning": "Dotted",
		"name":         `{"morning":"Nested"}`,
	}))
	if c.SlotNames["morning"] != "Dotted" {
		t.Errorf("SlotNames[morning] = %q, want Dotted", c.SlotNames["morning"])
	}
}

// The exact config a pre-0.9.7 server stores, taken from the measured run of
// the 0.9.6 manifest through JsonForms 2.5.2 and Navidrome's Ajv: every dotted
// key present, the untouched icon and colour fields holding the empty string
// their schema default put there, and only the three the admin edited holding
// anything. The empty ones must not override the built-in look.
func TestTheConfigAPreUpgradeServerActuallyStores(t *testing.T) {
	stored := map[string]string{
		"name.morning": "Jutro", "name.afternoon": "Afternoon",
		"name.evening": "Evening", "name.night": "Night",
		"name.rediscover": "Rediscover",
		"icon.morning":    "star", "color.morning": "AABBCC",
	}
	// Listed here rather than read off buttonFamilies: the test states what
	// the measured config held, so it must not move when that table does.
	untouched := []string{
		"afternoon", "evening", "night", "rediscover", "decade",
		"newmusic", "loved", "onrepeat", "essentials", "discovery",
		"genreradio", "artistradio", "dailymix", "wrapped",
	}
	for _, family := range untouched {
		stored["icon."+family] = ""
		stored["color."+family] = ""
	}
	c := Load(getterFrom(stored))
	if c.SlotNames["morning"] != "Jutro" {
		t.Errorf("the one name the admin typed was lost: %q", c.SlotNames["morning"])
	}
	if c.SlotNames["night"] != "Night" {
		t.Errorf("SlotNames[night] = %q, want Night", c.SlotNames["night"])
	}
	if icon, color := c.ButtonFor("morning"); icon != "star" || color != "AABBCC" {
		t.Errorf("morning button = %q/%q, want star/AABBCC", icon, color)
	}
	// Every family the admin never touched keeps its built-in icon, which is
	// the whole reason an unconfigured shelf still reads as several things.
	for _, family := range untouched {
		icon, color := c.ButtonFor(family)
		if icon != DefaultButtonIcons[family] {
			t.Errorf("%s icon = %q, want %q", family, icon, DefaultButtonIcons[family])
		}
		if color != DefaultButtonColors[icon] {
			t.Errorf("%s colour = %q, want %q", family, color, DefaultButtonColors[icon])
		}
	}
}

// A blob that is not JSON must cost nothing: the slot keeps its default rather
// than the plugin failing to load its configuration at all.
func TestAnUnreadableNestedBlobLeavesTheDefaults(t *testing.T) {
	for _, raw := range []string{"", "   ", "not json", "{", `{"morning":3}`, "[]"} {
		c := Load(getterFrom(map[string]string{"name": raw}))
		if c.SlotNames["morning"] != "Morning" {
			t.Errorf("name=%q gave %q, want Morning", raw, c.SlotNames["morning"])
		}
	}
}

// Nothing above may change a server that never touched any of these fields.
func TestAnUnconfiguredServerStillGetsTheBuiltInButtons(t *testing.T) {
	c := Load(getterFrom(nil))
	for family, want := range DefaultButtonIcons {
		if icon, _ := c.ButtonFor(family); icon != want {
			t.Errorf("%s icon = %q, want %q", family, icon, want)
		}
	}
	if c.PlaylistName("morning") != "\U0001F7E0 Morning" {
		t.Errorf("PlaylistName(morning) = %q", c.PlaylistName("morning"))
	}
}
