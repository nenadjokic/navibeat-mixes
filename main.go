// NaviBeat Mixes: a Navidrome plugin that builds listening-aware playlists.
//
// Three units with separate jobs, so a fault in one cannot take the others
// down:
//
//   - Collector (Scrobbler): watches plays and keeps an hour-of-day histogram.
//     It owns no playlist logic.
//   - Generator (SchedulerCallback): selects tracks and writes playlists. It
//     reads the histogram and never writes it.
//   - Lifecycle: registers the schedule on load.
//
// The Subsonic API exposes only a track's LAST play, never the individual
// plays, so hour-of-day affinity cannot be derived from the API at any price.
// Watching the scrobbles go past is the only route, which is why the plugin
// needs a warm-up period and says so in every playlist it writes.
package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/extism/go-pdk"

	"github.com/navidrome/navidrome/plugins/pdk/go/host"
	"github.com/navidrome/navidrome/plugins/pdk/go/lifecycle"
	"github.com/navidrome/navidrome/plugins/pdk/go/scheduler"
	"github.com/navidrome/navidrome/plugins/pdk/go/scrobbler"

	"github.com/nenadjokic/navibeat-mixes/internal/collector"
	"github.com/nenadjokic/navibeat-mixes/internal/config"
	"github.com/nenadjokic/navibeat-mixes/internal/library"
	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
	"github.com/nenadjokic/navibeat-mixes/internal/protocol"
)

const (
	scheduleID = "navibeat-mixes-daily"
	// 04:00 server time: after the listening day is over and before anyone is
	// awake to watch playlists rearrange themselves under them.
	scheduleCron = "0 4 * * *"

	// How many albums to pull per list when assembling the candidate pool.
	albumPages = 100
	// Cap on stored per-track history, so state cannot grow without bound on
	// a very large library.
	histTrackCap = 5000
	// One artist may contribute at most this many tracks to a mix. Thirty
	// tracks by one artist is an album, not a mix.
	maxPerArtist = 3
)

func init() {
	lifecycle.Register(&plugin{})
	scheduler.Register(&plugin{})
	// Capability is decided by which functions the WASM exports, not by
	// anything in the manifest. Registering here is what makes the host
	// deliver play events.
	scrobbler.Register(&plugin{})
}

type plugin struct{}

var (
	_ lifecycle.InitProvider     = (*plugin)(nil)
	_ scheduler.CallbackProvider = (*plugin)(nil)
)

// OnInit registers the daily schedule and generates once immediately, so a
// user who just installed the plugin sees playlists now rather than tomorrow
// morning. First impressions decide whether the plugin stays enabled.
func (p *plugin) OnInit() error {
	if _, err := host.SchedulerScheduleRecurring(scheduleCron, "generate", scheduleID); err != nil {
		logf("could not register the daily schedule: %v", err)
	}
	return generateAll()
}

// OnCallback runs the scheduled generation.
func (p *plugin) OnCallback(scheduler.SchedulerCallbackRequest) error {
	return generateAll()
}

// generateAll rebuilds every enabled mix for every user.
//
// Errors are logged and the run continues. One user with an odd library, or
// one mix that cannot be built, must not stop the others from being written.
func generateAll() error {
	cfg := config.Load(func(key string) string { v, _ := pdk.GetConfig(key); return v })

	users, err := host.UsersGetUsers()
	if err != nil || len(users) == 0 {
		admins, aerr := host.UsersGetAdmins()
		if aerr != nil || len(admins) == 0 {
			logf("no users available, nothing to generate")
			return nil
		}
		users = admins
	}

	for _, u := range users {
		if err := generateForUser(cfg, u.UserName); err != nil {
			logf("user %s: %v", u.UserName, err)
		}
	}
	return nil
}

func generateForUser(cfg config.Config, username string) error {
	client := library.New(host.SubsonicAPICall, username)

	tracks, err := client.Candidates(albumPages)
	if err != nil {
		return err
	}
	if len(tracks) == 0 {
		logf("user %s: no candidate tracks yet, skipping", username)
		return nil
	}
	tracks = mixes.FilterGenres(tracks, cfg.GenreDenylist, cfg.GenreNoiseThreshold)

	state := loadState(username)
	now := time.Now()

	if cfg.MixEnabled(string(mixes.Rediscover)) {
		sel := mixes.BuildRediscover(tracks, mixes.RediscoverOptions{
			Now:          now,
			MinAge:       time.Duration(cfg.RediscoverMonths) * 30 * 24 * time.Hour,
			RecentGrace:  30 * 24 * time.Hour,
			MinPlayCount: 3,
			Size:         cfg.MixSize,
		})
		writeMix(client, cfg, sel, now, username)
	}

	affinity := state.Affinity()
	for _, slot := range mixes.TimeSlots {
		if !cfg.MixEnabled(string(slot)) {
			continue
		}
		sel := mixes.BuildTimeMix(tracks, mixes.TimeMixOptions{
			Slot:                 slot,
			Affinity:             affinity,
			EventCount:           state.Events,
			MinEventsForAffinity: cfg.MinEventsForAffinity,
			Size:                 cfg.MixSize,
			MaxPerArtist:         maxPerArtist,
		})
		writeMix(client, cfg, sel, now, username)
	}
	return nil
}

// writeMix creates or updates one playlist. A mix that selected nothing is
// left alone rather than emptied: a user who opens a playlist they liked
// yesterday and finds it blank has lost something, and an empty playlist
// communicates a bug even when the truth is only "not enough data yet".
func writeMix(client *library.Client, cfg config.Config, sel mixes.Selection, now time.Time, username string) {
	if len(sel.TrackIDs) == 0 {
		logf("user %s: a mix selected nothing, leaving the existing playlist untouched", username)
		return
	}

	name := cfg.PlaylistName(string(sel.Slot))
	id, err := client.EnsurePlaylist(name)
	if err != nil {
		logf("user %s: %v", username, err)
		return
	}

	count, err := client.TrackCount(id)
	if err != nil {
		count = 0
	}
	if err := client.ReplaceTracks(id, count, sel.TrackIDs); err != nil {
		logf("user %s: writing tracks: %v", username, err)
		return
	}

	comment := protocol.Format(describe(cfg, sel), protocol.Meta{
		Kind:  kindFor(sel.Slot),
		Slot:  string(sel.Slot),
		Date:  now.Format("2006-01-02"),
		Mode:  string(sel.Mode),
		Count: len(sel.TrackIDs),
	})
	if err := client.SetComment(id, comment); err != nil {
		logf("user %s: writing description: %v", username, err)
	}
}

func kindFor(slot mixes.Slot) string {
	if slot == mixes.Rediscover {
		return "rediscover"
	}
	return "timeofday"
}

// describe writes the sentence a person reads. It states the mode in plain
// words, because a user whose mixes are not yet personal deserves to know that
// is why rather than concluding the feature is broken.
func describe(cfg config.Config, sel mixes.Selection) string {
	name := cfg.SlotNames[string(sel.Slot)]
	if sel.Slot == mixes.Rediscover {
		return "Music you loved and have not played in a while. Refreshes weekly."
	}
	if sel.Mode == mixes.ModeAffinity {
		return name + " mix, built from what you actually play at this time of day. Refreshes daily."
	}
	return name + " mix. Still learning when you listen to what, so for now this is built from your most played music. It gets more personal over the next few weeks."
}

// Collector: the Scrobbler half.

func (p *plugin) IsAuthorized(scrobbler.IsAuthorizedRequest) (bool, error) {
	// This gates delivery: returning false means no play events arrive at all.
	return true, nil
}

func (p *plugin) NowPlaying(scrobbler.NowPlayingRequest) error { return nil }

// Scrobble is the only event that carries a completed play, so it is the only
// one the histogram is built from.
func (p *plugin) Scrobble(req scrobbler.ScrobbleRequest) error {
	state := loadState(req.Username)
	at := time.Unix(req.Timestamp, 0)
	if req.Timestamp == 0 {
		at = time.Now()
	}
	accepted := state.Accept(collector.Event{
		TrackID:  req.Track.ID,
		Artist:   req.Track.Artist,
		At:       at,
		Duration: time.Duration(req.Track.Duration) * time.Second,
	})
	if !accepted {
		return nil
	}
	state.Prune(histTrackCap)
	saveState(req.Username, state)
	return nil
}

func (p *plugin) PlaybackReport(scrobbler.PlaybackReportRequest) error { return nil }

// State, held in the host key-value store.

func stateKey(username string) string { return "hist:" + username }

func loadState(username string) *collector.State {
	data, ok, err := host.KVStoreGet(stateKey(username))
	if err != nil || !ok {
		return collector.NewState()
	}
	return collector.Unmarshal(data)
}

func saveState(username string, s *collector.State) {
	data, err := s.Marshal()
	if err != nil {
		return
	}
	if err := host.KVStoreSet(stateKey(username), data); err != nil {
		logf("could not save listening history for %s: %v", username, err)
	}
}

func logf(format string, args ...any) {
	msg := format
	for _, a := range args {
		msg = replaceFirstVerb(msg, a)
	}
	pdk.Log(pdk.LogInfo, "[navibeat-mixes] "+msg)
}

// replaceFirstVerb is a tiny stand-in for fmt.Sprintf. The WASM binary ships
// to every user who installs the plugin, and pulling in the whole formatting
// machinery for a handful of log lines is not worth the bytes.
func replaceFirstVerb(s string, a any) string {
	i := strings.IndexByte(s, '%')
	if i < 0 || i+1 >= len(s) {
		return s
	}
	var val string
	switch v := a.(type) {
	case string:
		val = v
	case int:
		val = strconv.Itoa(v)
	case error:
		val = v.Error()
	default:
		val = "?"
	}
	return s[:i] + val + s[i+2:]
}

func main() {}
