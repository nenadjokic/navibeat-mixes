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
	// A mix has to be worth opening. Below this, the user simply has not
	// listened to enough for the plugin to say anything useful, and a four
	// track "Morning mix" is not a mix, it is clutter with a name on it.
	// Found on a real family server, where an account with almost no history
	// produced four-track playlists that cluttered the admin's view of their
	// own library.
	minMixSize = 10

	// The control playlist is polled far more often than mixes are generated,
	// because a person who presses "re-roll" is standing there waiting.
	controlScheduleID = "navibeat-mixes-control"
	controlCron       = "*/5 * * * *"
	controlName       = "NaviBeat control"
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
	if _, err := host.SchedulerScheduleRecurring(controlCron, "control", controlScheduleID); err != nil {
		logf("could not register the control poll: %v", err)
	}
	return generateAll()
}

// OnCallback runs whichever job fired. The control poll is deliberately a
// separate schedule from generation: a malformed command must not be able to
// stop the mixes from being refreshed, which is the whole reason the design
// keeps these two apart.
func (p *plugin) OnCallback(req scheduler.SchedulerCallbackRequest) error {
	if req.Payload == "control" {
		return pollControl()
	}
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

	// Every mix is described in one place, so adding one is a line here rather
	// than a new branch. Name and slot are FIXED per entry: nothing that varies
	// with the content ever reaches a playlist name, which is what stops the
	// library filling with orphans as rankings shift.
	type entry struct {
		key   string
		name  string
		slot  string
		human string
		kind  string
		sel   mixes.Selection
	}
	var plan []entry

	add := func(key, name, slot, human, kind string, sel mixes.Selection) {
		if !cfg.MixEnabled(key) {
			return
		}
		sel.Slot = mixes.Slot(slot)
		plan = append(plan, entry{key, cfg.Prefix + name, slot, human, kind, sel})
	}

	add("rediscover", cfg.SlotNames["rediscover"], "rediscover", "", "rediscover",
		mixes.BuildRediscover(tracks, mixes.RediscoverOptions{
			Now:          now,
			MinAge:       time.Duration(cfg.RediscoverMonths) * 30 * 24 * time.Hour,
			RecentGrace:  30 * 24 * time.Hour,
			MinPlayCount: 3,
			Size:         cfg.MixSize,
			RelaxFloor:   30 * 24 * time.Hour,
			MinUseful:    minMixSize,
		}))

	add("newmusic", "New Music", "newmusic",
		"NaviBeat Mixes: the newest additions to your library.", "newmusic",
		mixes.BuildNewMusic(tracks, cfg.MixSize, maxPerArtist))

	add("loved", "Your Loved Songs", "loved",
		"NaviBeat Mixes: everything you starred, oldest favourite first.", "loved",
		mixes.BuildLovedSongs(tracks, cfg.MixSize, maxPerArtist))

	add("onrepeat", "On Repeat", "onrepeat",
		"NaviBeat Mixes: what you have been playing over and over lately.", "onrepeat",
		mixes.BuildOnRepeat(tracks, now, 30*24*time.Hour, cfg.MixSize, maxPerArtist))

	add("essentials", "Your Essentials", "essentials",
		"NaviBeat Mixes: your most played of all time.", "essentials",
		mixes.BuildEssentials(tracks, cfg.MixSize, maxPerArtist))

	add("discovery", "Weekly Discovery", "discovery",
		"NaviBeat Mixes: music you own, played once or twice, and then forgot.", "discovery",
		mixes.BuildDiscovery(tracks, now, 90*24*time.Hour, cfg.MixSize, maxPerArtist))

	// Numbered mixes. The NUMBER is the identity and the subject goes in the
	// description, so the name never changes when the ranking does.
	// Weekly rotation. The pool is deliberately DEEPER than the window: with
	// 20 artists showing 5 at a time, a listener meets a different five every
	// week and the whole library comes round eventually, instead of staring at
	// the same top five forever. The name never moves, only what is inside it.
	week := mixes.WeekIndex(now)

	for i, g := range mixes.Rotate(mixes.TopGenres(tracks, 12), week, 3) {
		add("genreradio", "Genre Radio "+strconv.Itoa(i+1), mixes.NumberedSlot(mixes.GenreRadio, i+1),
			"NaviBeat Mixes: "+g+", one of the genres you play most. A different genre each week.", "genreradio",
			mixes.BuildForGenre(tracks, g, cfg.MixSize, maxPerArtist))
	}
	artists := mixes.Rotate(mixes.TopArtists(tracks, 20), week, 5)
	for i, a := range artists {
		add("artistradio", "Artist Radio "+strconv.Itoa(i+1), mixes.NumberedSlot(mixes.ArtistRadio, i+1),
			"NaviBeat Mixes: everything you have by "+a+". A different artist each week.", "artistradio",
			mixes.BuildForArtist(tracks, a, cfg.MixSize))
	}
	for i := 0; i < 3 && i < len(artists); i++ {
		add("dailymix", "Daily Mix "+strconv.Itoa(i+1), mixes.NumberedSlot(mixes.DailyMix, i+1),
			"NaviBeat Mixes: built around "+artists[i]+" and what sits near it. Rotates weekly.", "dailymix",
			mixes.BuildDailyMix(tracks, artists, i, cfg.MixSize, maxPerArtist))
	}
	for _, d := range mixes.Rotate(mixes.TopDecades(tracks, 6), week, 2) {
		label := strconv.Itoa(d) + "s"
		add("decade", label, "decade-"+strconv.Itoa(d),
			"NaviBeat Mixes: your "+label+", ranked by what you actually play. Rotates weekly.", "decade",
			mixes.BuildForDecade(tracks, d, cfg.MixSize, maxPerArtist))
	}

	if cfg.MixEnabled("wrapped") {
		year := mixes.YearOf(now)
		sel := mixes.BuildWrapped(tracks, mixes.WrappedOptions{
			Plays: state.TotalPlays(), Size: cfg.MixSize, MaxPerArtist: maxPerArtist,
		})
		sel.Slot = mixes.Slot(mixes.WrappedSlot(year))
		plan = append(plan, entry{"wrapped", cfg.Prefix + mixes.WrappedName(year), mixes.WrappedSlot(year),
			"NaviBeat Mixes: your most played, since the plugin was installed. Grows through the year.", "wrapped", sel})
	}

	affinity := state.Affinity()
	for _, slot := range mixes.TimeSlots {
		sel := mixes.BuildTimeMix(tracks, mixes.TimeMixOptions{
			Slot: slot, Affinity: affinity, EventCount: state.Events,
			MinEventsForAffinity: cfg.MinEventsForAffinity,
			Size:                 cfg.MixSize, MaxPerArtist: maxPerArtist,
		})
		add(string(slot), cfg.SlotNames[string(slot)], string(slot), describe(cfg, sel), "timeofday", sel)
	}

	for _, e := range plan {
		human := e.human
		if human == "" {
			human = describe(cfg, e.sel)
		}
		writeNamed(client, e.name, e.sel, human, e.kind, now, username)
	}
	return nil
}

// writeMix creates or updates one playlist. A mix that selected nothing is
// left alone rather than emptied: a user who opens a playlist they liked
// yesterday and finds it blank has lost something, and an empty playlist
// communicates a bug even when the truth is only "not enough data yet".
func writeMix(client *library.Client, cfg config.Config, sel mixes.Selection, now time.Time, username string) {
	if len(sel.TrackIDs) < minMixSize {
		logf("user %s: not enough listening yet for a useful mix, leaving it alone", username)
		return
	}

	writeNamed(client, cfg.PlaylistName(string(sel.Slot)), sel, describe(cfg, sel), kindFor(sel.Slot), now, username)
}

// writeNamed does the actual create-or-update for one playlist.
func writeNamed(client *library.Client, name string, sel mixes.Selection, human, kind string, now time.Time, username string) {
	if len(sel.TrackIDs) < minMixSize {
		return
	}
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

	comment := protocol.Format(human, protocol.Meta{
		Kind:  kind,
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

// describe writes the sentence a person reads.
//
// THE NAME COMES FIRST, and that is a decision taken from a screenshot rather
// than from taste. Navidrome's web UI truncates a playlist description to a
// single line, so an attribution sitting on line two is invisible in exactly
// the place most people look at these playlists. Leading with it is the only
// placement that survives truncation in every client.
//
// It still says the useful thing immediately afterwards: a user whose mixes
// are not yet personal deserves to know that is why, rather than concluding
// the feature is broken.
func describe(cfg config.Config, sel mixes.Selection) string {
	name := cfg.SlotNames[string(sel.Slot)]
	if sel.Slot == mixes.Rediscover {
		if sel.Relaxed {
			// Say so. A user who set six months and got six weeks deserves to
			// know the library could not fill it, rather than assuming the
			// setting was ignored.
			return "NaviBeat Mixes: the music you loved that you have left alone longest."
		}
		return "NaviBeat Mixes: music you loved and have not played in months."
	}
	if sel.Mode == mixes.ModeAffinity {
		return "NaviBeat Mixes: your " + strings.ToLower(name) + ", built from what you actually play at this time of day."
	}
	return "NaviBeat Mixes: your " + strings.ToLower(name) + ". Still learning when you listen, so for now this is your most played music."
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

// Control: the client-to-plugin mailbox.

// pollControl reads the control playlist, executes any command it finds, and
// writes back a result.
//
// There is no inbound channel to a plugin at all, so a playlist description is
// the only place a client can leave a message. That makes this the most
// exposed surface in the plugin: the field is editable by hand in every
// client, so it is treated as untrusted input throughout. Anything not fully
// understood is cleared rather than guessed at.
func pollControl() error {
	cfg := config.Load(func(key string) string { v, _ := pdk.GetConfig(key); return v })

	users, err := host.UsersGetUsers()
	if err != nil || len(users) == 0 {
		return nil
	}

	for _, u := range users {
		client := library.New(host.SubsonicAPICall, u.UserName)
		name := cfg.Prefix + controlName

		playlists, err := client.Playlists()
		if err != nil {
			continue
		}
		var target *library.Playlist
		for i := range playlists {
			if playlists[i].Name == name {
				target = &playlists[i]
				break
			}
		}
		if target == nil {
			// The client creates the mailbox (it has no way to know this
			// server's configured prefix), so a rename or a prefix change
			// must not orphan it: fall back to the machine line, which is
			// the part of the contract neither side can get wrong.
			for i := range playlists {
				if meta, ok := protocol.Parse(playlists[i].Comment); ok && meta.Kind == "control" {
					target = &playlists[i]
					break
				}
			}
		}
		if target == nil {
			// The mailbox is only created once a client asks for it, so an
			// unused server never grows a playlist nobody wanted.
			continue
		}

		cmd, ok := protocol.ParseCommand(target.Comment)
		if !ok {
			continue
		}

		// Nonce de-duplication. Without it the command sits in the playlist
		// and is executed again on every poll, turning one button press into
		// an endless regeneration loop.
		if lastNonce(u.UserName) == cmd.Nonce {
			continue
		}
		setLastNonce(u.UserName, cmd.Nonce)

		result := protocol.ResultDone
		if err := runCommand(cfg, u.UserName, cmd); err != nil {
			logf("control command failed: %v", err)
			result = protocol.ResultRejected
		}

		// Clear the command and leave the result in its place, so the mailbox
		// is empty and the client can see its request was handled.
		_ = client.SetComment(target.ID, protocol.Format(
			"NaviBeat uses this playlist to talk to the Mixes plugin. Safe to delete if you do not use NaviBeat.",
			protocol.Meta{Kind: "control", Slot: "control", Date: time.Now().Format("2006-01-02"), Mode: "ready", Count: 0},
		)+"\n"+protocol.FormatResult(result, cmd.Slot, cmd.Nonce))
	}
	return nil
}

func runCommand(cfg config.Config, username string, cmd protocol.Command) error {
	switch cmd.Kind {
	case protocol.CmdRefreshAll:
		return generateForUser(cfg, username)
	case protocol.CmdReroll:
		// Re-rolling one mix still runs the whole user pass. Generation is
		// cheap next to the round trips it already makes, and a partial pass
		// would need a second code path that could drift from the first.
		return generateForUser(cfg, username)
	}
	return nil
}

func nonceKey(username string) string { return "ctl:" + username + ":lastNonce" }

func lastNonce(username string) string {
	data, ok, err := host.KVStoreGet(nonceKey(username))
	if err != nil || !ok {
		return ""
	}
	return string(data)
}

func setLastNonce(username, nonce string) {
	_ = host.KVStoreSet(nonceKey(username), []byte(nonce))
}
