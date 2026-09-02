// Package config reads plugin settings, with a working default for every key.
//
// A plugin that only behaves well once configured is a plugin most people
// uninstall before configuring it, so every value here has a default that
// produces a good result on a server nobody has touched.
package config

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/nenadjokic/navibeat-mixes/internal/resume"
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
	// OnlyForUsers restricts which accounts get mixes at all. EMPTY MEANS
	// EVERYONE, which is what every existing install already does, so this
	// cannot change behaviour for anybody who does not fill it in.
	//
	// joshp23 (issue #3) on a multi-user server: mixes are built per user by
	// design, and an admin sees every playlist of every user, so a family
	// server ends up with a playlist list nobody can read. The clutter is the
	// visible half. The other half is cost: a run does one getStarred2, two
	// getAlbumList2 and 200 individual getAlbum calls PER USER, plus four or
	// five calls for each of the twenty or so playlists it writes. That is
	// about 300 Subsonic calls per user per run, most of them for people who
	// have never opened a mix.
	//
	// And the part that is not ours to spend: an account that never asked for
	// this gets twenty playlists dropped into their own library.
	OnlyForUsers []string
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
	// NewMusicOrder is what "new" means to the New Music mix: "added" ranks
	// on the date the file reached the server, "released" on the release
	// date in the tags. Steven O'Neil asked which one it was, on a library
	// where a record from 1984 ripped yesterday is the newest file and the
	// oldest music. Default "added", which is the only order there was
	// before 0.9.8, so an upgrade changes nothing for anybody.
	NewMusicOrder string
	// BudgetSeconds is how long one host call may work before the plugin
	// parks what it has and asks for a continuation. Navidrome stops any
	// plugin call at 30 seconds, so the ceiling stays well under that; the
	// floor exists because a budget shorter than one Subsonic page on a
	// slow server would continue forever and finish nothing.
	BudgetSeconds int
}

// Budget limits, in seconds. MaxBudgetSeconds leaves five seconds under the
// host's deadline for the step in flight plus the ledger and pool writes.
const (
	MinBudgetSeconds = 3
	MaxBudgetSeconds = 25
)

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
		MixStyle:      "cover",
		ButtonIcons:   map[string]string{},
		ButtonColors:  map[string]string{},
		NewMusicOrder: "added",
		BudgetSeconds: int(resume.DefaultLimit / time.Second),
	}
}

// nameSlots is every mix whose displayed name the user can change, with the
// camel case suffix of its config key. Iterating a slice rather than the
// SlotNames map keeps the read order fixed, which a map range does not.
var nameSlots = []struct{ key, suffix string }{
	{"morning", "Morning"}, {"afternoon", "Afternoon"},
	{"evening", "Evening"}, {"night", "Night"},
	{"rediscover", "Rediscover"},
}

// buttonFamilies is every mix family that can carry an icon and a colour
// override. The key is the slot for time-of-day mixes and the kind for
// everything else, which is exactly what ButtonFor resolves against. The
// suffix matches the enable<Family> switches the same settings form already
// renders, so the whole schema reads one way.
var buttonFamilies = []struct{ key, suffix string }{
	{"morning", "Morning"}, {"afternoon", "Afternoon"},
	{"evening", "Evening"}, {"night", "Night"},
	{"rediscover", "Rediscover"}, {"decade", "Decade"},
	{"newmusic", "NewMusic"}, {"loved", "Loved"},
	{"onrepeat", "OnRepeat"}, {"essentials", "Essentials"},
	{"discovery", "Discovery"}, {"genreradio", "GenreRadio"},
	{"artistradio", "ArtistRadio"}, {"dailymix", "DailyMix"},
	{"wrapped", "Wrapped"},
}

// slotGroup reads one per-slot setting in all three shapes it can arrive in.
//
// A DOT in a config key is what FunkeCoder23 reported in issue #5: the Name
// fields in Navidrome's plugin settings were empty, typing into them did not
// stick, and no icon or colour was ever applied. Navidrome renders the schema
// with JsonForms, whose toDataPath turns the scope "#/properties/name.morning"
// into the data path "name.morning", and whose resolveData then SPLITS that
// path on '.'. Measured against @jsonforms/core 2.5.2, the version Navidrome's
// ui pins:
//
//	toDataPath("#/properties/name.morning")                  -> "name.morning"
//	Resolve.data({"name.morning":"Morning"}, "name.morning") -> undefined
//
// So the control looked for a nested object, found nothing, and rendered an
// empty box. The box stays empty because the renderer is fully controlled by
// that same resolved value: OutlinedRenderers.jsx:107 binds the TextField's
// value straight to it, so a typed character does not survive the next render.
//
// Which shape a pre-0.9.7 server ended up storing was also measured, against
// the real 0.9.6 manifest and Navidrome's own Ajv (useDefaults: true, set in
// ui/src/plugin/SchemaConfigEditor.jsx:44 and handed to JsonForms at :225).
// Every one of the 35 dotted properties carried a schema default, so Ajv wrote
// the LITERAL dotted key into the form data before any control rendered. From
// then on lodash treats "name.morning" as one key rather than a path, and the
// save comes back FLAT:
//
//	stored {} -> Ajv -> {"name.morning":"Morning", "icon.morning":"", ...}
//	user edits    -> saved {"name.morning":"Jutro","icon.morning":"star", ...}
//
// That is why the dotted key is read BEFORE the nested blob: on a stock server
// the flat dotted key is the value the owner typed, and a nested blob can only
// come from somewhere else, in which case it is the older of the two. The
// nested shape is read at all because Navidrome's parsePluginConfig keeps only
// top-level keys and JSON-serializes anything nested, so a config holding one
// reaches this plugin as config["name"] = "{\"morning\":\"Jutro\"}".
//
// The keys are dot free from 0.9.7 on. Both older shapes stay readable because
// a server configured before the rename must not lose what its owner typed.
type slotGroup struct {
	get    Getter
	group  string // "name", "icon" or "color", the top-level key of the blob
	nested map[string]string
	read   bool
}

// value resolves one slot: the dot free key first, then the old dotted key,
// then the slot inside the nested blob.
func (g *slotGroup) value(field, slot string) string {
	if v := strings.TrimSpace(g.get(field)); v != "" {
		return v
	}
	if v := strings.TrimSpace(g.get(g.group + "." + slot)); v != "" {
		return v
	}
	if !g.read {
		// Parsed once per group, not once per slot: a blob that is absent or
		// unreadable must cost one look, not fifteen.
		g.read = true
		raw := strings.TrimSpace(g.get(g.group))
		if strings.HasPrefix(raw, "{") {
			var m map[string]string
			if json.Unmarshal([]byte(raw), &m) == nil {
				g.nested = make(map[string]string, len(m))
				for k, v := range m {
					g.nested[strings.ToLower(strings.TrimSpace(k))] = v
				}
			}
		}
	}
	return strings.TrimSpace(g.nested[slot])
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
	if v := strings.TrimSpace(get("onlyForUsers")); v != "" {
		c.OnlyForUsers = splitList(v)
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
	names := &slotGroup{get: get, group: "name"}
	for _, s := range nameSlots {
		if v := names.value("name"+s.suffix, s.key); v != "" {
			c.SlotNames[s.key] = v
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
	// What New Music ranks on. Same rule as mixStyle: a value that is not one
	// of the two words is ignored, so a typo keeps today's order rather than
	// producing a mix ranked on nothing.
	if v := strings.ToLower(strings.TrimSpace(get("newMusicOrder"))); v != "" {
		switch v {
		case "added", "released":
			c.NewMusicOrder = v
		}
	}
	// The per-call budget. Clamped rather than rejected: a person who types
	// 60 on a slow server means "give it more time", and 25 is the most that
	// can be given under the host's 30 second deadline.
	if n, ok := positiveInt(get("budgetSeconds")); ok {
		if n < MinBudgetSeconds {
			n = MinBudgetSeconds
		}
		if n > MaxBudgetSeconds {
			n = MaxBudgetSeconds
		}
		c.BudgetSeconds = n
	}
	// Per-family button overrides, read through the same three shapes as the
	// names above.
	icons := &slotGroup{get: get, group: "icon"}
	colors := &slotGroup{get: get, group: "color"}
	for _, f := range buttonFamilies {
		if v := icons.value("icon"+f.suffix, f.key); v != "" {
			c.ButtonIcons[f.key] = v
		}
		if v := colors.value("color"+f.suffix, f.key); v != "" {
			c.ButtonColors[f.key] = v
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

// BuildsFor reports whether this account should get mixes at all.
//
// An empty OnlyForUsers means everyone, so an install that never opens this
// field behaves exactly as it did before the field existed.
//
// The comparison is case insensitive on purpose. An admin filling in a list of
// their own users types names from memory, and "Josh" instead of "josh" would
// otherwise produce a server where the plugin silently does nothing at all,
// with no error to read anywhere. Navidrome usernames are unique regardless, so
// folding case cannot match the wrong person.
func (c Config) BuildsFor(username string) bool {
	if len(c.OnlyForUsers) == 0 {
		return true
	}
	for _, allowed := range c.OnlyForUsers {
		if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(username)) {
			return true
		}
	}
	return false
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
