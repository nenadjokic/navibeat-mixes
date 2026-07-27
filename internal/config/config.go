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
	// SlotNames maps a slot key to the human name used in the playlist title,
	// so the mixes can speak the user's language.
	SlotNames map[string]string
}

// Defaults returns the configuration used when nothing is set.
func Defaults() Config {
	return Config{
		Prefix:               "\U0001F7E0 ", // large orange circle, the NaviBeat brand colour
		MixSize:              30,
		RediscoverMonths:     6,
		MinEventsForAffinity: 150,
		GenreDenylist:        []string{"Music", "Musik", "Musique"},
		GenreNoiseThreshold:  0.6,
		EnabledMixes:         nil,
		SlotNames: map[string]string{
			"morning":    "Morning",
			"afternoon":  "Afternoon",
			"evening":    "Evening",
			"night":      "Night",
			"rediscover": "Rediscover",
		},
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
	for slot := range c.SlotNames {
		if v := strings.TrimSpace(get("name." + slot)); v != "" {
			c.SlotNames[slot] = v
		}
	}
	return c
}

// MixEnabled reports whether a mix key should be generated.
func (c Config) MixEnabled(key string) bool {
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
