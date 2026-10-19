// Package library is the thin layer between the plugin and the Subsonic API.
//
// It is deliberately dumb: fetch, decode, hand back plain structs. Every
// decision about which tracks belong in a mix lives in the mixes package,
// where it can be tested without a server.
package library

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
	"github.com/nenadjokic/navibeat-mixes/internal/protocol"
)

// Caller performs a Subsonic request and returns the raw JSON body. Injected
// so this package can be exercised against recorded responses.
type Caller func(uri string) (string, error)

// Client talks to one Subsonic server as one user.
type Client struct {
	call Caller
	user string
	// playlists is the getPlaylists answer, fetched once per client and kept
	// for EnsurePlaylist. Before this, every one of the ~30 mixes written for a
	// user re-fetched the whole list (109 playlists on the server that showed
	// it), which was a third of the nightly run's Subsonic calls and part of
	// why the run overran the host's deadline. nil means not fetched yet.
	playlists []Playlist
}

// New returns a client that acts as the given user. Every Subsonic call needs
// a user to act as, which is why the plugin requires the users permission.
func New(call Caller, user string) *Client {
	return &Client{call: call, user: user}
}

// User is the account this client acts as.
//
// Exposed because callers outside this package have to filter `getPlaylists`
// by owner, and that filtering is not optional: see the comment on
// EnsurePlaylist for what the server actually returns.
func (c *Client) User() string { return c.user }

type song struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Genre  string `json:"genre"`
	// OpenSubsonic's multi-value genre list. The legacy `genre` above carries
	// only ONE, so a track tagged "Hip-Hop; Funk" reports Hip-Hop and nothing
	// else, and every genre-shaped mix silently missed it. Servers that do not
	// implement the extension simply send nothing here and the legacy field
	// still works. Spec: opensubsonic.netlify.app/docs/responses/child/
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Year      int    `json:"year"`
	PlayCount int    `json:"playCount"`
	Played    string `json:"played"`
	Starred   string `json:"starred"`
	Duration  int    `json:"duration"`
	// Created is when the file entered the library. Every Subsonic Child
	// carries it and Navidrome always fills it in
	// (server/subsonic/helpers.go, mediaFileCreatedAt), so New Music can rank
	// on a real date instead of guessing from the order the pool arrived in.
	Created string `json:"created"`
}

// genreNames flattens the OpenSubsonic array. Returns nil when the server did
// not send one, which is the signal the mixes package uses to fall back to the
// legacy single value.
func (s song) genreNames() []string {
	if len(s.Genres) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Genres))
	for _, g := range s.Genres {
		if g.Name != "" {
			out = append(out, g.Name)
		}
	}
	return out
}

type envelope struct {
	Response struct {
		Status string `json:"status"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Starred2 struct {
			Song []song `json:"song"`
		} `json:"starred2"`
		AlbumList2 struct {
			Album []struct {
				ID string `json:"id"`
			} `json:"album"`
		} `json:"albumList2"`
		// MusicFolders is the getMusicFolders answer: the libraries this
		// account can see. Navidrome writes the id as a number and a string is
		// accepted too, so a server that quotes it still parses (#502717).
		MusicFolders struct {
			MusicFolder []struct {
				ID   flexID `json:"id"`
				Name string `json:"name"`
			} `json:"musicFolder"`
		} `json:"musicFolders"`
		Album         album `json:"album"`
		SearchResult3 struct {
			Song []song `json:"song"`
		} `json:"searchResult3"`
		Playlists struct {
			Playlist []Playlist `json:"playlist"`
		} `json:"playlists"`
		Playlist Playlist `json:"playlist"`
	} `json:"subsonic-response"`
}

// itemDate is OpenSubsonic's date object: any of the three parts may be
// missing, and a wholly untagged album arrives as `{}`. Navidrome fills it
// from a "YYYY-MM-DD", "YYYY-MM" or "YYYY" tag (server/subsonic/helpers.go,
// toItemDate), so a year-only tag is {"year":2025} and nothing else.
type itemDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// album is the part of a getAlbum answer the pool needs. The date fields are
// album-level on purpose: a Subsonic song carries `year` and nothing finer,
// while the AlbumID3 the same call already returns carries the full release
// dates (helpers.go, buildOSAlbumID3), so reading them here costs no extra
// round trip. Measured on a real 0.63.2: `year`, `created`, `releaseDate`
// and `originalReleaseDate` are present on every album, the two date objects
// empty when the library carries no such tags.
type album struct {
	Year                int      `json:"year"`
	ReleaseDate         itemDate `json:"releaseDate"`
	OriginalReleaseDate itemDate `json:"originalReleaseDate"`
	Song                []song   `json:"song"`
}

// Playlist is the subset of a Subsonic playlist this plugin uses.
type Playlist struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Comment   string `json:"comment"`
	Owner     string `json:"owner"`
	SongCount int    `json:"songCount"`
	// Changed and Created are the timestamps getPlaylists sends (Navidrome's
	// buildPlaylist fills both). Changed is what picks the survivor when two
	// playlists claim the same mix, see EnsureMix.
	Changed string `json:"changed"`
	Created string `json:"created"`
}

func (c *Client) do(endpoint string, params url.Values) (*envelope, error) {
	params.Set("u", c.user)
	uri := endpoint + "?" + params.Encode()
	raw, err := c.call(uri)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", endpoint, err)
	}
	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("%s: decoding response: %w", endpoint, err)
	}
	// Navidrome answers HTTP 200 with a failure inside the body, so the error
	// has to be read out of the JSON rather than inferred from the transport.
	if env.Response.Status != "ok" {
		if e := env.Response.Error; e != nil {
			return nil, fmt.Errorf("%s: %d %s", endpoint, e.Code, e.Message)
		}
		return nil, fmt.Errorf("%s: failed with no detail", endpoint)
	}
	return &env, nil
}

// flexID is an id that may arrive as a JSON number or as a JSON string and
// is kept as the string the Subsonic query wants back.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	*f = flexID(strings.Trim(string(b), `"`))
	return nil
}

// AccessibleLibraries is the list of library ids this account may read, from
// getMusicFolders. The pool uses it to drop a configured id the server would
// refuse (#502717).
func (c *Client) AccessibleLibraries() ([]string, error) {
	env, err := c.do("getMusicFolders", url.Values{})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(env.Response.MusicFolders.MusicFolder))
	for _, f := range env.Response.MusicFolders.MusicFolder {
		if f.ID != "" {
			ids = append(ids, string(f.ID))
		}
	}
	return ids, nil
}

// CandidateOptions says how the pool is assembled. The zero value is exactly
// what Candidates has always done.
type CandidateOptions struct {
	// AlbumPages is how many albums to pull per album list.
	AlbumPages int
	// ByReleaseDate adds a fourth album list, the albums released in Year and
	// the two years before it, so a New Music mix ranked on release date has
	// this year's records in its pool whatever day they were added. Without
	// it the released order could only reorder the recently ADDED albums:
	// measured on a real server, the six newest-added albums included 1984
	// and 1991 releases, while releases from the previous year, added months
	// earlier, were not in the pool at all.
	ByReleaseDate bool
	// Year is the current year, the top of that window.
	Year int
	// SkipStarred folds getStarred2 out of the run and marks that phase done.
	// It is the last thing a killed run drops before giving up (#502323):
	// getStarred2 takes no size parameter in the Subsonic API, so it is the
	// one call on this path that cannot be made smaller by asking
	// differently. The cost is coverage rather than correctness, since every
	// album fetch carries each track's starred flag too, so a starred track
	// whose album is in one of the lists is still in the pool.
	SkipStarred bool
	// MusicFolderIDs restricts the pool to these Navidrome libraries, sent as
	// a repeated `musicFolderId` on getStarred2 and on every getAlbumList2.
	// Empty sends nothing, which the server reads as every library the
	// account may see, the behaviour every existing install already has
	// (#502335). Read in the Navidrome source (server/subsonic/helpers.go,
	// selectedMusicFolderIds, v0.64.2): an id outside the account's
	// libraries is NOT dropped, it fails the call with "Library N not found
	// or not accessible". So Assemble asks getMusicFolders first and sends
	// only the configured ids the account can see (#502717). When it can see
	// none of them, Assemble fetches nothing and returns an empty pool
	// (decision D40 A, 0.9.17) rather than widening to every library.
	MusicFolderIDs []string
}

// releaseWindowYears is how far back the release-date list reaches, counted
// from Year inclusive: this year and the two before it.
const releaseWindowYears = 2

// Candidates gathers the pool the mixes are selected from: everything the user
// starred, plus the tracks of their newest, most played and most recent albums.
//
// There is no "give me every song" call in Subsonic that is safe on a large
// library, so the pool is assembled from the endpoints that are cheap and are
// already biased towards music the user cares about.
//
// ⛔ `newest` IS LOAD BEARING AND ITS ABSENCE WAS A DAY ONE BUG. The pool was
// built from `frequent` and `recent` alone, and both of those are play-ordered:
// on a server installed this morning they return NOTHING. So a new user with a
// fully scanned library of thousands of tracks got an empty pool, one line in
// the log, and not a single playlist, which is exactly how it was reported.
// `newest` is the only one of the three that answers on a library nobody has
// played yet, which also makes it the honest source for New Music.
func (c *Client) Candidates(albumPages int) ([]mixes.Track, error) {
	return c.CandidatesWith(CandidateOptions{AlbumPages: albumPages})
}

// CandidatesWith is Candidates with the pool options spelled out. It fetches
// the whole pool in one go; the plugin itself goes through Assemble so the
// fetch can stop at the budget and continue in a later call.
func (c *Client) CandidatesWith(opts CandidateOptions) ([]mixes.Track, error) {
	p, err := c.Assemble(opts, nil, nil)
	if err != nil {
		return nil, err
	}
	return p.Tracks(), nil
}

// Release date precision, for refining a track across passes. A higher value
// is a more exact date.
const (
	releaseUnknown = iota
	releaseYear
	releaseMonth
	releaseDay
)

// releasedFrom picks the release date for one song, and how exact it is.
//
// Precedence: the album's original release date, then its release date, then
// the album's `year`, then the song's own `year`. Original first because that
// is the order Navidrome itself ranks by, and because a 2015 remaster of a
// 1975 record is not new music; with the historic tagging Navidrome maps
// (ORIGINALDATE=1975, DATE=2015) it lands as year 1975, releaseDate 2015, and
// the original wins. Anything before 1900 is treated as untagged, the same
// floor the decade mixes use.
func releasedFrom(al *album, s song) (time.Time, int) {
	if al != nil {
		for _, d := range []itemDate{al.OriginalReleaseDate, al.ReleaseDate} {
			if d.Year >= 1900 {
				return dateOf(d)
			}
		}
		if al.Year >= 1900 {
			return dateOf(itemDate{Year: al.Year})
		}
	}
	if s.Year >= 1900 {
		return dateOf(itemDate{Year: s.Year})
	}
	return time.Time{}, releaseUnknown
}

// dateOf turns a possibly partial date into a time, with the missing parts
// set to 1, and reports how many parts were present.
func dateOf(d itemDate) (time.Time, int) {
	exact := releaseYear
	month, day := 1, 1
	if d.Month >= 1 && d.Month <= 12 {
		month = d.Month
		exact = releaseMonth
		if d.Day >= 1 && d.Day <= 31 {
			day = d.Day
			exact = releaseDay
		}
	}
	return time.Date(d.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC), exact
}

// Playlists lists the playlists visible to this user.
func (c *Client) Playlists() ([]Playlist, error) {
	env, err := c.do("getPlaylists", url.Values{})
	if err != nil {
		return nil, err
	}
	return env.Response.Playlists.Playlist, nil
}

// EnsurePlaylist finds this user's playlist by name, or creates it.
//
// THE OWNER CHECK IS LOAD BEARING ON A MULTI-USER SERVER, and its absence was
// a real bug found on one. `getPlaylists` returns every playlist the caller
// can see, which includes other people's public ones. Matching on name alone
// meant the second user to run would find the FIRST user's "Morning" mix and
// quietly overwrite it with their own taste in music.
//
// Mixes are per user by design, so two people are supposed to end up with two
// playlists of the same name. Only the owner may be handed back their own.
func (c *Client) EnsurePlaylist(name string) (string, error) {
	if err := c.load(); err != nil {
		return "", err
	}
	for _, p := range c.playlists {
		if p.Name == name && p.Owner == c.user {
			return p.ID, nil
		}
	}
	return c.create(name)
}

// load fetches getPlaylists once per client and keeps it.
func (c *Client) load() error {
	if c.playlists != nil {
		return nil
	}
	existing, err := c.Playlists()
	if err != nil {
		return err
	}
	c.playlists = existing
	return nil
}

// create makes a new, non-public playlist and records it in the cache.
func (c *Client) create(name string) (string, error) {
	// Created non-public: these are personal mixes, and a server with five
	// accounts should not show everyone five copies of "Morning".
	env, err := c.do("createPlaylist", url.Values{"name": {name}})
	if err != nil {
		return "", err
	}
	id := env.Response.Playlist.ID
	if id == "" {
		return "", fmt.Errorf("createPlaylist %q returned no id", name)
	}
	c.playlists = append(c.playlists, Playlist{ID: id, Name: name, Owner: c.user})
	if _, err := c.do("updatePlaylist", url.Values{
		"playlistId": {id}, "public": {"false"},
	}); err != nil {
		// Not fatal: a mix that is visible to others still works, it is just
		// untidy, and failing here would cost the user the whole playlist.
		//
		// ⛔ BUT IT IS NO LONGER SILENT. This error was discarded outright, and
		// that discard is the reason one question from a multi-user server
		// could not be answered from a log: if a user reports seeing other
		// people's mixes, this call failing is the first suspect and there was
		// nothing written down to confirm or clear it.
		if Logf != nil {
			logf("could not make %s private: %v", name, err)
		}
	}
	return id, nil
}

// EnsureMix finds this user's playlist for one mix and makes sure it carries
// the given name, or creates it. The mix is identified by (kind, slot), the
// pair in the machine line of the description, and NOT by its name.
//
// WHY: the name used to be the only key. Changing the prefix therefore
// stopped the old playlist being found and a second one was created next to
// it, orphaning the first. A playlist's id and its marker survive a rename, so
// they are what a mix is looked up by now.
//
// Order of lookup, all of it among THIS user's playlists only:
//
//  1. The marker, ranked (see findByMarker). Others carrying the same marker
//     (a copy the user made, a duplicate) are left exactly as they are: this
//     plugin never deletes, and they are not ours to judge.
//  2. Adoption of a playlist with NO marker, for playlists written before the
//     marker was trusted as identity: one whose name equals the wanted name
//     exactly, as EnsurePlaylist always did. A playlist whose marker names a
//     DIFFERENT mix is never adopted: it has an identity, and taking it would
//     steal it from its own slot.
//  3. Nothing found: create it. created is then true.
//
// When the stored name is not the wanted name the playlist is renamed in
// place (updatePlaylist, parameter name), so the id never changes. A mix the
// user renamed by hand is renamed back the same way. A failed rename is logged
// and does not fail the write: the tracks and description still go in and the
// old name stays until the next run. If another of this user's playlists
// already has the target name the rename still happens (Navidrome allows
// duplicate names) but one line names both ids.
//
// ⛔ THE OWNER CHECK IS LOAD BEARING, exactly as in EnsurePlaylist and
// FindControl: an admin's getPlaylists holds every user's playlists, and a
// marker is identical across users. Matching without the owner would rename
// and overwrite somebody else's mix.
func (c *Client) EnsureMix(kind, slot, name string) (id string, created bool, err error) {
	if err := c.load(); err != nil {
		return "", false, err
	}
	idx := c.findByMarker(kind, slot, name)
	if idx < 0 {
		idx = c.findToAdopt(kind, slot, name)
	}
	if idx < 0 {
		id, err := c.create(name)
		return id, err == nil, err
	}
	p := c.playlists[idx]
	if p.Name != name {
		for _, o := range c.playlists {
			if o.Owner == c.user && o.ID != p.ID && o.Name == name {
				logf("renaming playlist %s to %q, which playlist %s already has", p.ID, name, o.ID)
				break
			}
		}
		if _, err := c.do("updatePlaylist", url.Values{
			"playlistId": {p.ID}, "name": {name},
		}); err != nil {
			logf("could not rename %q to %q: %v", p.Name, name, err)
		} else {
			c.playlists[idx].Name = name
		}
	}
	return p.ID, false, nil
}

// findToAdopt returns the index of an unmarked playlist of this user whose
// name equals the wanted name, or -1. See step 2 of EnsureMix. A playlist that
// carries the marker of another mix is skipped.
func (c *Client) findToAdopt(kind, slot, name string) int {
	for i, p := range c.playlists {
		if p.Owner != c.user || p.Name != name {
			continue
		}
		if meta, ok := protocol.Parse(p.Comment); ok && (meta.Kind != kind || meta.Slot != slot) {
			continue
		}
		return i
	}
	return -1
}

// findByMarker returns the index in c.playlists of this user's playlist for
// (kind, slot), or -1. When several carry the marker (a copy the user made
// keeps it too) they are ranked:
//
//  1. its name equals the wanted name,
//  2. otherwise the most recently changed, then the most recently created,
//     then the first listed.
//
// Within a tier the same recency order breaks ties. The ranking is what stops a
// duplicate the user made, and is editing, from taking over the mix because it
// happens to be newer than the real one.
func (c *Client) findByMarker(kind, slot, name string) int {
	best := -1
	var bestTier int
	var bestChanged, bestCreated time.Time
	for i, p := range c.playlists {
		if p.Owner != c.user {
			continue
		}
		meta, ok := protocol.Parse(p.Comment)
		if !ok || meta.Kind != kind || meta.Slot != slot {
			continue
		}
		tier := 2
		if p.Name == name {
			tier = 1
		}
		changed, created := parseTime(p.Changed), parseTime(p.Created)
		// Strictly better wins, so on a full tie the first listed is kept and
		// the answer does not depend on anything but the server's order.
		better := best < 0 || tier < bestTier ||
			(tier == bestTier && (changed.After(bestChanged) ||
				(changed.Equal(bestChanged) && created.After(bestCreated))))
		if better {
			best, bestTier, bestChanged, bestCreated = i, tier, changed, created
		}
	}
	return best
}

// FindControl picks THIS user's control mailbox out of what the server
// returned, and never anybody else's.
//
// ⛔ THE OWNER CHECK IS THE POINT OF THIS FUNCTION. `getPlaylists` returns every
// playlist the caller can SEE, and what that means depends on the account.
// Navidrome's own filter, read from source rather than guessed
// (persistence/playlist_repository.go, userFilter):
//
//	if user.IsAdmin { return And{} }          // no filter at all
//	return Or{ Eq{"public": true}, Eq{"owner_id": user.ID} }
//
// So for an ADMIN the list holds every playlist of every user on the server,
// private ones included. Matching the mailbox by name alone therefore hands an
// admin the FIRST user's mailbox, and the machine-line fallback was worse: it
// matched the first control playlist anywhere on the server, which on any
// multi-user install is somebody else's.
//
// That is not a cosmetic mix-up. The caller reads a nonce out of this playlist
// and EXECUTES the command it carries, so without this check an admin could run
// another user's queued command and then write the reply back over that user's
// mailbox.
//
// EnsurePlaylist has carried the same check since a multi-user server exposed
// this shape for the mixes themselves. These two loops were simply missed.
func FindControl(playlists []Playlist, name, owner string) *Playlist {
	for i := range playlists {
		if playlists[i].Name == name && playlists[i].Owner == owner {
			return &playlists[i]
		}
	}
	// The client creates the mailbox (it has no way to know this server's
	// configured prefix), so a rename or a prefix change must not orphan it:
	// fall back to the machine line, which is the part of the contract neither
	// side can get wrong. Still only among this user's own playlists.
	for i := range playlists {
		if playlists[i].Owner != owner {
			continue
		}
		if meta, ok := protocol.Parse(playlists[i].Comment); ok && meta.Kind == "control" {
			return &playlists[i]
		}
	}
	return nil
}

// Logf, when set, receives warnings this package chooses not to fail on.
//
// A function variable rather than an import because this package is kept free
// of the plugin SDK so its tests run as ordinary Go.
var Logf func(format string, args ...any)

// logf is Logf with the nil check that every caller would otherwise have to
// repeat and one of them forgot. The package's own tests never set Logf, so
// an unguarded call turns a warning into a panic in exactly the code path a
// test was written to cover.
func logf(format string, args ...any) {
	if Logf != nil {
		Logf(format, args...)
	}
}

// WriteMix writes the tracks and the description (which holds the marker) of
// a mix whose playlist EnsureMix returned.
//
// ORDER DEPENDS ON created, and that is the point of the flag. The marker is
// how a mix is found again, and the description is the only place it lives. A
// playlist that was just created has no marker yet, so if the run stopped after
// the tracks but before the description, a later run that no longer sees the
// same name (a changed prefix) would create a SECOND playlist and the first
// would be orphaned for good. So for a created
// playlist the description goes in FIRST (createPlaylist with playlistId, used
// by ReplaceTracks, keeps the stored comment), and if it cannot be written
// the error is returned so the caller treats the slot as failed and retries.
// One immediate retry covers a transient failure without a second pass.
//
// An existing playlist already has its marker, so there the order stays
// tracks first, then description, and a description that fails to write is
// only logged: rewriting the same tracks next pass to fix a sentence costs
// more than the stale sentence is worth.
func (c *Client) WriteMix(id string, created bool, trackIDs []string, comment string) error {
	if created {
		err := c.SetComment(id, comment)
		if err != nil {
			err = c.SetComment(id, comment)
		}
		if err != nil {
			return fmt.Errorf("writing description of new playlist %s: %w", id, err)
		}
		return c.ReplaceTracks(id, trackIDs)
	}
	if err := c.ReplaceTracks(id, trackIDs); err != nil {
		return err
	}
	if err := c.SetComment(id, comment); err != nil {
		logf("writing description of %s: %v", id, err)
	}
	return nil
}

// ReplaceTracks sets a playlist's contents to exactly the given ids.
//
// It uses createPlaylist with playlistId, which Navidrome treats as "replace
// every track of this playlist": core/playlists/playlists.go Create loads the
// playlist, empties its track list and stores the new one, and
// persistence/playlist_repository.go updatePlaylist deletes every
// playlist_tracks row for the id before inserting. Name, comment and public
// come back from the stored row and are written unchanged.
//
// navibeat-mixes#8: this used updatePlaylist with songIndexToRemove 0..n-1,
// where n was getPlaylist's songCount. That count only includes rows that are
// not missing and that the caller's libraries can see, while the removal is by
// position over every row. So a missing file, or a track from a library the
// account no longer reads, sat at the head of the playlist and survived every
// rewrite, and more of them piled up each night.
//
// The server checks that the caller owns the playlist (or is an admin) and
// refuses a smart or synced playlist. EnsureMix and EnsurePlaylist only hand back
// playlists owned by this client's user, so the first holds. An empty list is
// never sent: Navidrome's Put skips the track rewrite when there are no tracks,
// so it would leave the old contents in place, and writeNamed already refuses
// a mix under minMixSize.
func (c *Client) ReplaceTracks(playlistID string, trackIDs []string) error {
	if len(trackIDs) == 0 {
		return fmt.Errorf("replace %s: no tracks to write", playlistID)
	}
	params := url.Values{"playlistId": {playlistID}}
	for _, id := range trackIDs {
		params.Add("songId", id)
	}
	_, err := c.do("createPlaylist", params)
	return err
}

// SetComment writes the playlist description.
func (c *Client) SetComment(playlistID, comment string) error {
	_, err := c.do("updatePlaylist", url.Values{
		"playlistId": {playlistID}, "comment": {comment},
	})
	if err == nil {
		// Keep the cache honest: the marker just written is what EnsureMix
		// looks a mix up by.
		for i := range c.playlists {
			if c.playlists[i].ID == playlistID {
				c.playlists[i].Comment = comment
			}
		}
	}
	return err
}

// parseTime accepts the ISO timestamps Navidrome emits and returns the zero
// time for anything missing or unparsable, which callers read as "never".
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
