// Package config reads plugin settings, with a working default for every key.
//
// A plugin that only behaves well once configured is a plugin most people
// uninstall before configuring it, so every value here has a default that
// produces a good result on a server nobody has touched.
package config

import (
	"strconv"
	"strings"
)

// Getter reads a raw config value. It exists so the whole package is testable
// without a WASM host: production passes pdk.GetConfig, tests pass a map.
type Getter func(key string) string

// Config is the resolved settings for one run.
type Config struct {
	// Prefix goes in front of every playlist name. Configurable because a
	// user may already use the same emoji for something else, so detection on
	// the client side keys on the description marker and treats this as a
	// display hint only.
	Prefix string
	// MixSize is how many tracks each mix holds.
	MixSize int
	// MaxPerArtist caps how many tracks one artist may contribute to a mix.
	// Sly777 (issue #1): "on genre radios, plugin adds 3-4 songs per specific
	// album from the specific musician. I would like to have 1 song per
	// musician." Both wants are legitimate and they contradict each other, so
	// it is a setting rather than a new hardcoded number. Default 3 keeps
	// every existing install byte-identical.
	MaxPerArtist int
	// RediscoverMonths is how old a track's last play must be before
	// Rediscover will consider it.
	RediscoverMonths int
	// MinEventsForAffinity is how many observed plays a user needs before
	// time-of-day mixes stop guessing from popularity and start using the
	// real histogram.
	MinEventsForAffinity int
	// GenreDenylist are genres carrying no signal, ignored during selection.
	GenreDenylist []string
	// GenreNoiseThreshold is the share of the library above which a genre is
	// treated as noise regardless of the denylist. A genre covering almost
	// everything cannot discriminate between tracks.
	GenreNoiseThreshold float64
	// EnabledMixes lists the mix keys to generate. Empty means all of them.
	EnabledMixes []string
	// WrappedSharing opts in to sending an aggregate recap for a shareable
	// link. Off unless the user turns it on, and it is the ONLY thing in this
	// plugin that can send anything anywhere.
	WrappedSharing bool
	// MixSwitches holds the per-mix toggles the settings form renders, keyed
	// by slot. Absent means the user never touched that switch.
	MixSwitches map[string]bool
	// SlotNames maps a slot key to the human name used in the playlist title,
	// so the mixes can speak the user's language.
	SlotNames map[string]string
	// MixStyle is how NaviBeat clients should DRAW these playlists: the
	// generated NaviBeat artwork, the server's own album mosaic, or a compact
	// row of buttons.
	//
	// This lives here rather than in the NaviBeat app on purpose. A setting
	// inside the app for a feature that lives in this plugin is a dead switch
	// for everyone who never installed the plugin, which is exactly what the
	// app's old "NaviBeat covers" toggle became.
	MixStyle string
	// ButtonIcons and ButtonColors override the built-in look of one mix
	// family's button, keyed by mix kind ("timeofday" uses the SLOT instead,
	// because all four share one kind and would otherwise get one icon).
	// Absent means "use the built-in default", which is why an untouched
	// server still gets a varied shelf.
	ButtonIcons  map[string]string
	ButtonColors map[string]string
}

// DefaultButtonIcons is the built-in icon per mix family, and the reason a
// server nobody has configured still gets a shelf that reads as several
// different things rather than one thing repeated. That repetition is the
// complaint this whole feature came from.
//
// Time of day is keyed by SLOT, not kind: all four time-of-day mixes share
// kind "timeofday", so keying by kind would give morning and night the same
// sun and rebuild the sameness.
var DefaultButtonIcons = map[string]string{
	"morning":     "sunrise",
	"afternoon":   "sun",
	"evening":     "sunset",
	"night":       "moon",
	"rediscover":  "clock",
	"decade":      "clock",
	"newmusic":    "sparkles",
	"loved":       "heart",
	"onrepeat":    "repeat",
	"essentials":  "star",
	"discovery":   "compass",
	"genreradio":  "radio",
	"artistradio": "radio",
	"dailymix":    "shuffle",
	"wrapped":     "gift",
}

// DefaultButtonColors matches the Apple client's own defaults, so a mix looks
// the same on a phone whether or not the server ever set a colour.
var DefaultButtonColors = map[string]string{
	"sunrise":  "F2A65A",
	"sun":      "E8A317",
	"sunset":   "E2725B",
	"moon":     "6C7BA8",
	"sparkles": "7C5CFF",
	"compass":  "2E9E8F",
	"heart":    "E0567A",
	"star":     "D4A017",
	"repeat":   "4C8DD9",
	"radio":    "3F9E5A",
	"shuffle":  "9B6BD6",
	"clock":    "8A8577",
	"gift":     "D96BA0",
	"waveform": "6E8AA6",
}

// ButtonFor resolves the icon and colour for one playlist.
//
// `key` is the slot for time-of-day mixes and the kind for everything else;
// the caller passes whichever identifies the family, and a user override on
// that key wins over the built-in.
func (c Config) ButtonFor(key string) (icon, color string) {
	key = strings.ToLower(strings.TrimSpace(key))
	icon = strings.ToLower(strings.TrimSpace(c.ButtonIcons[key]))
	if icon == "" {
		icon = DefaultButtonIcons[key]
	}
	if icon == "" {
		icon = "waveform"
	}
	color = normaliseHex(c.ButtonColors[key])
	if color == "" {
		color = DefaultButtonColors[icon]
	}
	if color == "" {
		color = DefaultButtonColors["waveform"]
	}
	return icon, color
}

// normaliseHex accepts what a person actually types into a colour field and
// returns six uppercase hex digits, or "" when it cannot. A leading '#' is the
// commonest input and the one thing the wire format does NOT allow, so
// stripping it here is worth more than rejecting it.
func normaliseHex(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		// "F00" is what a CSS-literate user types.
		var expanded strings.Builder
		for _, r := range s {
			expanded.WriteRune(r)
			expanded.WriteRune(r)
		}
		s = expanded.String()
	}
	if len(s) != 6 {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
		default:
			return ""
		}
	}
	return s
}

// Defaults returns the configuration used when nothing is set.
func Defaults() Config {
	return Config{
		Prefix:               "\U0001F7E0 ", // large orange circle, the NaviBeat brand colour
		MixSize:              30,
		MaxPerArtist:         3,
		RediscoverMonths:     6,
		MinEventsForAffinity: 150,
		GenreDenylist:        []string{"Music", "Musik", "Musique"},
		GenreNoiseThreshold:  0.6,
		EnabledMixes:         nil,
		WrappedSharing:       false,
		MixSwitches:          map[string]bool{},
		SlotNames: map[string]string{
			"morning":    "Morning",
			"afternoon":  "Afternoon",
			"evening":    "Evening",
			"night":      "Night",
			"rediscover": "Rediscover",
		},
		// The default is the artwork every existing install already sees, so
		// upgrading the plugin never changes how anybody's shelf looks.
		MixStyle:     "cover",
		ButtonIcons:  map[string]string{},
		ButtonColors: map[string]string{},
	}
}

// Load applies whatever the user set on top of the defaults. A value that does
// not parse is ignored rather than fatal: a typo in one setting must not stop
// the whole plugin from producing playlists.
func Load(get Getter) Config {
	c := Defaults()
	if v := strings.TrimSpace(get("prefix")); v != "" {
		// A prefix is usually written without the trailing space in a config
		// field, but reads badly without one in a playlist name.
		if !strings.HasSuffix(v, " ") {
			v += " "
		}
		c.Prefix = v
	}
	if n, ok := positiveInt(get("mixSize")); ok {
		c.MixSize = n
	}
	if n, ok := positiveInt(get("maxPerArtist")); ok {
		c.MaxPerArtist = n
	}
	if n, ok := positiveInt(get("rediscoverMonths")); ok {
		c.RediscoverMonths = n
	}
	if n, ok := positiveInt(get("minEventsForAffinity")); ok {
		c.MinEventsForAffinity = n
	}
	if v := strings.TrimSpace(get("genreDenylist")); v != "" {
		c.GenreDenylist = splitList(v)
	}
	if v := strings.TrimSpace(get("genreNoiseThreshold")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			c.GenreNoiseThreshold = f
		}
	}
	if v := strings.TrimSpace(get("enabledMixes")); v != "" {
		c.EnabledMixes = splitList(v)
	}
	// Deliberately strict: only an explicit "true" enables it. A typo must
	// leave sharing off, never on.
	if strings.EqualFold(strings.TrimSpace(get("wrappedSharing")), "true") {
		c.WrappedSharing = true
	}
	for slot := range c.SlotNames {
		if v := strings.TrimSpace(get("name." + slot)); v != "" {
			c.SlotNames[slot] = v
		}
	}
	// Per-mix switches. Only an explicit value counts: an unset switch must
	// leave the mix at its default rather than silently disabling it.
	for _, m := range []struct{ key, field string }{
		{"morning", "enableMorning"}, {"afternoon", "enableAfternoon"},
		{"evening", "enableEvening"}, {"night", "enableNight"},
		{"rediscover", "enableRediscover"}, {"wrapped", "enableWrapped"},
		{"newmusic", "enableNewMusic"}, {"loved", "enableLoved"},
		{"onrepeat", "enableOnRepeat"}, {"essentials", "enableEssentials"},
		{"discovery", "enableDiscovery"}, {"genreradio", "enableGenreRadio"},
		{"artistradio", "enableArtistRadio"}, {"dailymix", "enableDailyMix"},
		{"decade", "enableDecade"},
	} {
		v := strings.TrimSpace(get(m.field))
		if v == "" {
			continue
		}
		c.MixSwitches[m.key] = strings.EqualFold(v, "true")
	}
	// How clients should draw the mixes. An unrecognised value is ignored
	// rather than written to the wire: a typo here would otherwise reach every
	// client as an instruction none of them understand.
	if v := strings.ToLower(strings.TrimSpace(get("mixStyle"))); v != "" {
		switch v {
		case "cover", "button", "mosaic":
			c.MixStyle = v
		}
	}
	// Per-family button overrides. Keyed by slot for time-of-day (all four
	// share one kind) and by kind for everything else, which is exactly the
	// key ButtonFor resolves against.
	for _, key := range []string{
		"morning", "afternoon", "evening", "night",
		"rediscover", "decade", "newmusic", "loved", "onrepeat",
		"essentials", "discovery", "genreradio", "artistradio",
		"dailymix", "wrapped",
	} {
		if v := strings.TrimSpace(get("icon." + key)); v != "" {
			c.ButtonIcons[key] = v
		}
		if v := strings.TrimSpace(get("color." + key)); v != "" {
			c.ButtonColors[key] = v
		}
	}
	return c
}

// MixEnabled reports whether a mix key should be generated.
//
// Two sources, and the per-mix switches win. The manifest renders one toggle
// per mix, which is what a person actually sees and edits; `enabledMixes` is
// the older comma-separated key and stays honoured so nobody's existing
// configuration silently changes meaning under them after an upgrade.
func (c Config) MixEnabled(key string) bool {
	if on, set := c.MixSwitches[strings.ToLower(key)]; set {
		return on
	}
	if len(c.EnabledMixes) == 0 {
		return true
	}
	for _, k := range c.EnabledMixes {
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return true
		}
	}
	return false
}

// PlaylistName is the full, FIXED name for a mix. It never embeds variable
// data such as an artist or a date: a name that changes with the content
// creates a brand new playlist every time the content shifts, which is how a
// library ends up littered with orphans that need a cleanup job.
func (c Config) PlaylistName(slot string) string {
	name, ok := c.SlotNames[slot]
	if !ok {
		name = slot
	}
	return c.Prefix + name
}

func positiveInt(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
