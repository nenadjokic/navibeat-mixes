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
	"fmt"
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
	"github.com/nenadjokic/navibeat-mixes/internal/resume"
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

	// A run that does not fit in one host call continues in another one a few
	// seconds later. See internal/resume for why this exists at all.
	continueScheduleID = "navibeat-mixes-continue"
	continueDelaySec   = 5
	ledgerKey          = "run:ledger"
)

func init() {
	lifecycle.Register(&plugin{})
	scheduler.Register(&plugin{})
	// Capability is decided by which functions the WASM exports, not by
	// anything in the manifest. Registering here is what makes the host
	// deliver play events.
	scrobbler.Register(&plugin{})
	// The library package keeps itself free of the plugin SDK so its tests run
	// as ordinary Go, so it reaches the host log through this instead.
	library.Logf = logf
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
	// 0.9.4: a load is a fresh start. 0.9.1's ledger made an upgrade on the
	// same day report "generation complete in 0s" and build nothing, because
	// the morning's run had already marked every user done. Someone who just
	// installed or upgraded the plugin is owed a full run now, so the day's
	// ledger is forgotten before it starts.
	saveLedger(resume.NewLedger(resume.DayOf(time.Now())))
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

// generateAll rebuilds every enabled mix for every user, within the host's
// call budget, and continues in a later call when it cannot finish.
//
// Errors are logged and the run continues. One user with an odd library, or
// one mix that cannot be built, must not stop the others from being written.
//
// Found on a live server (2026-08-21): Navidrome kills a scheduler callback
// after about 30 seconds ("module closed with context deadline exceeded"),
// and a five-account server with one large library cost more than that, so
// every night only the first part of the plan was refreshed and the
// time-of-day mixes, last in the plan, never were. The run now keeps a
// per-day ledger of what it has written, stops when its time budget is
// spent, and asks the host to call it again in a few seconds; the next call
// skips everything already in the ledger.
func generateAll() error {
	budget := resume.StartBudget(resume.DefaultLimit, time.Now)
	day := resume.DayOf(time.Now())
	ledger := loadLedger(day)
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

	skipped := 0
	for _, u := range users {
		// The whole point of the setting is HERE, before the work rather than
		// after it: a skipped user costs about 300 Subsonic calls less, which
		// on a five account server is most of the run.
		if !cfg.BuildsFor(u.UserName) {
			skipped++
			continue
		}
		if ledger.UserDone(u.UserName) {
			continue
		}
		finished, err := generateForUser(cfg, u.UserName, ledger, budget)
		if err != nil {
			logf("user %s: %v", u.UserName, err)
		}
		saveLedger(ledger)
		if !finished {
			// Out of time with work left: hand the rest to the next call
			// rather than be killed mid-write with nothing to say about it.
			if _, err := host.SchedulerScheduleOneTime(continueDelaySec, "generate", continueScheduleID); err != nil {
				logf("could not schedule the continuation: %v", err)
				return nil
			}
			logf("budget of %v spent after %v with %s unfinished, continuing in %ds",
				resume.DefaultLimit, budget.Elapsed().Round(time.Second), u.UserName, continueDelaySec)
			return nil
		}
	}
	if skipped > 0 {
		// Said out loud once per run. A setting that silently stops the plugin
		// from doing anything is the setting people file bugs about, and a
		// typo in the list is exactly how that happens.
		logf("skipped %d user(s): not in the configured account list", skipped)
	}
	logf("generation complete for %s in %v", day, budget.Elapsed().Round(time.Second))
	return nil
}

func loadLedger(day string) *resume.Ledger {
	data, ok, err := host.KVStoreGet(ledgerKey)
	if err != nil || !ok {
		return resume.NewLedger(day)
	}
	return resume.Decode(data, day)
}

func saveLedger(l *resume.Ledger) {
	if err := host.KVStoreSet(ledgerKey, l.Encode()); err != nil {
		logf("could not save the run ledger: %v", err)
	}
}

// generateForUser writes one user's plan, skipping what the ledger already
// holds for today and stopping when the budget is spent. Returns whether the
// user is finished; false means "call again".
func generateForUser(cfg config.Config, username string, ledger *resume.Ledger, budget *resume.Budget) (bool, error) {
	client := library.New(host.SubsonicAPICall, username)

	// The release-date list is fetched only when New Music is enabled and set
	// to rank on it: it is the one consumer, and it costs up to albumPages
	// more getAlbum calls per user per run.
	byRelease := cfg.MixEnabled("newmusic") && cfg.NewMusicOrder == string(mixes.NewMusicByReleased)
	tracks, err := client.CandidatesWith(library.CandidateOptions{
		AlbumPages:    albumPages,
		ByReleaseDate: byRelease,
		Year:          time.Now().Year(),
	})
	if err != nil {
		return true, err
	}
	if len(tracks) == 0 {
		logf("user %s: no candidate tracks yet, skipping", username)
		ledger.MarkUserDone(username)
		return true, nil
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

	// The name and the slot never change with the order; only the description
	// says which "new" this is, and the description is free to change.
	newMusicHuman := "NaviBeat Mixes: the newest additions to your library."
	if byRelease {
		newMusicHuman = "NaviBeat Mixes: the newest releases in your library."
	}
	add("newmusic", "New Music", "newmusic", newMusicHuman, "newmusic",
		mixes.BuildNewMusicBy(tracks, mixes.NewMusicOrder(cfg.NewMusicOrder), cfg.MixSize, cfg.MaxPerArtist))

	add("loved", "Your Loved Songs", "loved",
		"NaviBeat Mixes: everything you starred, oldest favourite first.", "loved",
		mixes.BuildLovedSongs(tracks, cfg.MixSize, cfg.MaxPerArtist))

	add("onrepeat", "On Repeat", "onrepeat",
		"NaviBeat Mixes: what you have been playing over and over lately.", "onrepeat",
		mixes.BuildOnRepeat(tracks, now, 30*24*time.Hour, cfg.MixSize, cfg.MaxPerArtist))

	add("essentials", "Your Essentials", "essentials",
		"NaviBeat Mixes: your most played of all time.", "essentials",
		mixes.BuildEssentials(tracks, cfg.MixSize, cfg.MaxPerArtist))

	add("discovery", "Weekly Discovery", "discovery",
		"NaviBeat Mixes: music you own, played once or twice, and then forgot.", "discovery",
		mixes.BuildDiscovery(tracks, now, 90*24*time.Hour, cfg.MixSize, cfg.MaxPerArtist))

	// Numbered mixes. The NUMBER is the identity and the subject goes in the
	// description, so the name never changes when the ranking does.
	// Weekly rotation. The pool is deliberately DEEPER than the window: with
	// 20 artists showing 5 at a time, a listener meets a different five every
	// week and the whole library comes round eventually, instead of staring at
	// the same top five forever. The name never moves, only what is inside it.
	week := mixes.WeekIndex(now)

	// Same floor and same reason as the artist pool below: a genre that cannot
	// fill a mix must not be given a number it will then fail to use.
	for i, g := range mixes.Rotate(mixes.TopGenresOwning(tracks, 12, minMixSize, cfg.MaxPerArtist), week, 3) {
		add("genreradio", "Genre Radio "+strconv.Itoa(i+1), mixes.NumberedSlot(mixes.GenreRadio, i+1),
			"NaviBeat Mixes: "+g+", one of the genres you play most. A different genre each week.", "genreradio",
			mixes.BuildForGenre(tracks, g, cfg.MixSize, cfg.MaxPerArtist))
	}
	// The 20 are the artists that can actually FILL an Artist Radio: an artist
	// with fewer than minMixSize tracks in the pool would be numbered, then
	// silently skipped by writeNamed, and its number would be missing from the
	// user's library (issue #4). The floor is applied inside TopArtistsOwning
	// BEFORE the top 20 is taken, so anybody with 20 eligible artists keeps a
	// pool of exactly 20 and their weekly rotation does not move.
	artists := mixes.Rotate(mixes.TopArtistsOwning(tracks, 20, minMixSize), week, 5)
	for i, a := range artists {
		add("artistradio", "Artist Radio "+strconv.Itoa(i+1), mixes.NumberedSlot(mixes.ArtistRadio, i+1),
			"NaviBeat Mixes: everything you have by "+a+". A different artist each week.", "artistradio",
			mixes.BuildForArtist(tracks, a, cfg.MixSize))
	}
	for i := 0; i < 3 && i < len(artists); i++ {
		add("dailymix", "Daily Mix "+strconv.Itoa(i+1), mixes.NumberedSlot(mixes.DailyMix, i+1),
			"NaviBeat Mixes: built around "+artists[i]+" and what sits near it. Rotates weekly.", "dailymix",
			mixes.BuildDailyMix(tracks, artists, i, cfg.MixSize, cfg.MaxPerArtist))
	}
	for _, d := range mixes.Rotate(mixes.TopDecades(tracks, 6), week, 2) {
		label := strconv.Itoa(d) + "s"
		add("decade", label, "decade-"+strconv.Itoa(d),
			"NaviBeat Mixes: your "+label+", ranked by what you actually play. Rotates weekly.", "decade",
			mixes.BuildForDecade(tracks, d, cfg.MixSize, cfg.MaxPerArtist))
	}

	if cfg.MixEnabled("wrapped") {
		year := mixes.YearOf(now)
		sel := mixes.BuildWrapped(tracks, mixes.WrappedOptions{
			Plays: state.TotalPlays(), Size: cfg.MixSize, MaxPerArtist: cfg.MaxPerArtist,
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
			Size:                 cfg.MixSize, MaxPerArtist: cfg.MaxPerArtist,
			// 0.9.3: a different slice of the slot's pool each day.
			Day: mixes.DayIndex(now),
		})
		add(string(slot), cfg.SlotNames[string(slot)], string(slot), describe(cfg, sel), "timeofday", sel)
	}

	written := 0
	for _, e := range plan {
		if ledger.SlotDone(username, e.slot) {
			continue
		}
		if budget.Spent() {
			if written > 0 || len(ledger.DoneSlots(username)) > 0 {
				logf("user %s: %d of %d mixes written so far, the rest continue in the next call",
					username, len(ledger.DoneSlots(username)), len(plan))
			}
			return false, nil
		}
		human := e.human
		if human == "" {
			human = describe(cfg, e.sel)
		}
		// ⛔ "NOTHING WAS WRITTEN" HIDES TWO OPPOSITE MEANINGS, which is why
		// this asks the outcome two separate questions.
		//
		// Is the slot settled for today? A real write is. So is a selection
		// that came out under minMixSize, because that is a fact about today's
		// candidate pool and not about the server: the next continuation pass
		// would rebuild the same plan, reach the same answer, and log the same
		// line. Recording it is what the code did before this fix and it stays.
		//
		// A failed host call is NOT settled. That was the real hole: a
		// createPlaylist or ReplaceTracks that failed once was marked done and
		// never retried for the rest of the day. It now stays out of the
		// ledger, so the next pass tries it again.
		//
		// The retry cannot spin. MarkUserDone below runs whatever happens, so
		// the day still ends, and every slot that succeeds drops out of the
		// next pass, so successive passes get shorter rather than longer.
		outcome := writeNamed(client, cfg, e.name, e.sel, human, e.kind, now, username)
		if outcome != writeFailed {
			ledger.MarkSlotDone(username, e.slot)
		}
		if outcome == writeOK {
			written++
		}
	}
	// #F531: publish the style in the same pass that wrote the mixes, so a
	// setting change lands everywhere at once instead of the tiles moving now
	// and the style following on the next five-minute poll.
	if target := controlPlaylistFor(client, cfg); target != nil {
		publishStyle(client, cfg, target)
	}
	ledger.MarkUserDone(username)
	return true, nil
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

	writeNamed(client, cfg, cfg.PlaylistName(string(sel.Slot)), sel, describe(cfg, sel), kindFor(sel.Slot), now, username)
}

// publishStyle writes the presentation style onto the control playlist, and is
// deliberately callable from BOTH the daily generation and the five-minute
// control poll.
//
// Found while verifying 0.7.0 on a live server: publishing only from the poll
// means a user who changes the setting sees the mixes regenerate immediately
// but the STYLE arrive up to five minutes later, which reads as "it did not
// work". Writing it from the generation run too closes that window, and the
// write is idempotent so the poll doing it again costs nothing.
//
// Only writes when it actually differs: the poll runs every five minutes, and
// an unconditional SetComment there would be a database write per user forever.
func publishStyle(client *library.Client, cfg config.Config, target *library.Playlist) {
	if current, ok := protocol.ParseStyle(target.Comment); ok && string(current) == cfg.MixStyle {
		return
	}
	updated := protocol.AppendLine(target.Comment, protocol.FormatStyle(protocol.Style(cfg.MixStyle)))
	if err := client.SetComment(target.ID, updated); err != nil {
		logf("publishing the mix style: %v", err)
		return
	}
	target.Comment = updated
}

// controlPlaylistFor finds this user's control playlist: by the configured name
// first, then by the machine line, because the client creates the mailbox and
// cannot know the server's configured prefix.
func controlPlaylistFor(client *library.Client, cfg config.Config) *library.Playlist {
	playlists, err := client.Playlists()
	if err != nil {
		return nil
	}
	return library.FindControl(playlists, cfg.Prefix+controlName, client.User())
}

// writeOutcome is what one attempt at one playlist did.
//
// THREE VALUES AND NOT TWO, because the caller has to answer two different
// questions and a bool can only answer one: "did a playlist get written" and
// "is this slot settled for today". A skip for being too small answers no to
// the first and YES to the second. Modelled as a named type with a switchable
// set of values rather than a second bool, because a pair of bools at a call
// site does not say which is which, and this repo already states its small
// closed sets this way (mixes.Mode, protocol.ResultKind).
//
// writeFailed is deliberately the zero value: a result nobody set must never
// mark a slot done.
type writeOutcome int

const (
	// writeFailed: a host call failed. Transient as far as this plugin can
	// tell, so the slot stays out of the ledger and a later pass retries it.
	writeFailed writeOutcome = iota
	// writeTooSmall: the selection was under minMixSize. Deterministic for
	// today's candidate pool, so it is recorded as done for today and the
	// log line is written once rather than once per continuation pass.
	writeTooSmall
	// writeOK: the playlist was created or updated.
	writeOK
)

// writeNamed does the actual create-or-update for one playlist. It reports
// which of the three outcomes happened, so the caller can keep the run ledger
// honest instead of recording a write that never happened.
//
// ⛔ THE SKIP IS NOT SILENT ANY MORE, AND THAT WAS A REAL REPORT. SinTan1729
// (issue #4) saw Artist Radio and Daily Mix numbering start at 2 and wrote
// "I don't see any useful info in the logs", which was exactly right: the twin
// of this guard in writeMix does log, but writeMix has no callers, so every
// numbered mix came through here and this branch said nothing at all. One line
// naming the playlist and its count against the threshold is what turns a
// missing number into something a person can read off a log.
func writeNamed(client *library.Client, cfg config.Config, name string, sel mixes.Selection, human, kind string, now time.Time, username string) writeOutcome {
	if len(sel.TrackIDs) < minMixSize {
		logf("user %s: %s has %d tracks, under the %d a mix needs, so it was left alone",
			username, name, len(sel.TrackIDs), minMixSize)
		return writeTooSmall
	}
	id, err := client.EnsurePlaylist(name)
	if err != nil {
		logf("user %s: %v", username, err)
		return writeFailed
	}

	count, err := client.TrackCount(id)
	if err != nil {
		count = 0
	}
	if err := client.ReplaceTracks(id, count, sel.TrackIDs); err != nil {
		logf("user %s: writing tracks: %v", username, err)
		return writeFailed
	}

	comment := protocol.Format(human, protocol.Meta{
		Kind:  kind,
		Slot:  string(sel.Slot),
		Date:  now.Format("2006-01-02"),
		Mode:  string(sel.Mode),
		Count: len(sel.TrackIDs),
	})
	// #F531: tell NaviBeat how to draw this one, on its own line so the
	// descriptor above still parses on every client already in the field.
	//
	// ONLY when buttons are actually switched on. This is not an optimisation,
	// it is the thing that keeps the rollout safe: a NaviBeat old enough not to
	// know the nbui1 namespace renders any line it does not recognise as part
	// of the description, so writing a button line on a server that is not
	// using buttons would show "nbui1:btn:sunrise:F2A65A:Morning" to every one
	// of those users for no benefit at all. A server that opts in accepts that
	// trade knowingly; a server that never touches the setting must not pay it.
	//
	// The key is the SLOT for time-of-day mixes and the KIND for everything
	// else: all four time-of-day mixes share one kind, so keying by kind would
	// give morning and night the same sun and rebuild exactly the sameness
	// this feature exists to remove.
	if cfg.MixStyle == string(protocol.StyleButton) {
		buttonKey := kind
		if kind == "timeofday" {
			buttonKey = string(sel.Slot)
		}
		icon, colour := cfg.ButtonFor(buttonKey)
		// The label drops the prefix the playlist name carries: a button is too
		// narrow to spend on an emoji that only exists to group a list.
		//
		// SlotNames only holds the five slots that have a configurable name
		// (morning, afternoon, evening, night, rediscover), so for the other
		// twenty mixes it is empty. Falling back to the playlist name with the
		// prefix stripped is what makes those buttons read "New Music" instead
		// of the emoji-prefixed name a client would otherwise use. Found while
		// verifying 0.7.0 on a real server: every mix except those five came
		// out with an empty label.
		label := cfg.SlotNames[string(sel.Slot)]
		if label == "" {
			label = strings.TrimSpace(strings.TrimPrefix(name, cfg.Prefix))
		}
		comment = protocol.AppendLine(comment, protocol.FormatButton(protocol.Button{
			Glyph: icon, Color: colour, Label: label,
		}))
	}
	if err := client.SetComment(id, comment); err != nil {
		// The tracks are already in the playlist, so this IS a write. Failing
		// the slot here would rewrite the same tracks on the next pass to fix
		// a description, which costs more than the missing line is worth.
		logf("user %s: writing description: %v", username, err)
	}
	return writeOK
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
//
// DELIBERATELY NOT FILTERED BY `onlyForUsers`, and this is a choice rather than
// an oversight. Collecting costs one small key-value read and write per play,
// while GENERATING costs about 300 Subsonic calls per user per run, so the
// saving is all on the other side. Keeping the histogram for everyone means an
// account added to the list later starts with real evidence instead of serving
// popularity for weeks while it warms up. The setting decides who gets
// playlists, not who is worth remembering.
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
	case int64:
		val = strconv.FormatInt(v, 10)
	case time.Duration:
		// 0.9.1 logged "budget of ? spent after ?" because this switch did
		// not know a Duration. The one line that explains a continuation
		// must carry its numbers.
		val = v.String()
	case fmt.Stringer:
		val = v.String()
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
		// An account the plugin does not build for has no mixes, and both
		// commands a client can send (reroll one, refresh all) act on mixes.
		// So there is nothing here to act on, and skipping saves a
		// getPlaylists call per user on every poll.
		if !cfg.BuildsFor(u.UserName) {
			continue
		}
		client := library.New(host.SubsonicAPICall, u.UserName)
		name := cfg.Prefix + controlName

		playlists, err := client.Playlists()
		if err != nil {
			continue
		}
		// Owner-filtered, and that matters most HERE: this loop reads a nonce
		// and executes the command it finds. See library.FindControl.
		target := library.FindControl(playlists, name, u.UserName)
		if target == nil {
			// The mailbox is only created once a client asks for it, so an
			// unused server never grows a playlist nobody wanted.
			continue
		}

		// #F531: the control playlist is where the server-wide style lives.
		// Published here rather than on a mix because clients find this
		// playlist by machine line and EXCLUDE it from the shelf, so this line
		// is never rendered to a person even by a client too old to know it.
		//
		// Written only when it actually differs, because this poll runs every
		// five minutes and a needless SetComment on each one would be a write
		// to every user's database forever.
		publishStyle(client, cfg, target)

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
		// The comment is rebuilt from scratch here, so the style line has to be
		// put back or every executed command would silently un-configure the
		// shelf until the next poll noticed.
		rebuilt := protocol.Format(
			"NaviBeat uses this playlist to talk to the Mixes plugin. Safe to delete if you do not use NaviBeat.",
			protocol.Meta{Kind: "control", Slot: "control", Date: time.Now().Format("2006-01-02"), Mode: "ready", Count: 0},
		) + "\n" + protocol.FormatResult(result, cmd.Slot, cmd.Nonce)
		rebuilt = protocol.AppendLine(rebuilt, protocol.FormatStyle(protocol.Style(cfg.MixStyle)))
		_ = client.SetComment(target.ID, rebuilt)
	}
	return nil
}

func runCommand(cfg config.Config, username string, cmd protocol.Command) error {
	switch cmd.Kind {
	case protocol.CmdRefreshAll:
		return regenerateUserNow(cfg, username)
	case protocol.CmdReroll:
		// Re-rolling one mix still runs the whole user pass. Generation is
		// cheap next to the round trips it already makes, and a partial pass
		// would need a second code path that could drift from the first.
		return regenerateUserNow(cfg, username)
	}
	return nil
}

// regenerateUserNow is the control playlist's path: the user asked, so today's
// ledger for them is forgotten and the whole plan is rebuilt, under the same
// budget as the nightly run and with the same continuation if it runs long.
func regenerateUserNow(cfg config.Config, username string) error {
	day := resume.DayOf(time.Now())
	ledger := loadLedger(day)
	ledger.ResetUser(username)
	budget := resume.StartBudget(resume.DefaultLimit, time.Now)
	finished, err := generateForUser(cfg, username, ledger, budget)
	saveLedger(ledger)
	if !finished {
		if _, serr := host.SchedulerScheduleOneTime(continueDelaySec, "generate", continueScheduleID); serr != nil {
			logf("could not schedule the continuation: %v", serr)
		}
	}
	return err
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
