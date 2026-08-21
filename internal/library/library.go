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
	"strconv"
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
	ID        string `json:"id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Genre     string `json:"genre"`
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
		Album struct {
			Song []song `json:"song"`
		} `json:"album"`
		SearchResult3 struct {
			Song []song `json:"song"`
		} `json:"searchResult3"`
		Playlists struct {
			Playlist []Playlist `json:"playlist"`
		} `json:"playlists"`
		Playlist Playlist `json:"playlist"`
	} `json:"subsonic-response"`
}

// Playlist is the subset of a Subsonic playlist this plugin uses.
type Playlist struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Comment   string `json:"comment"`
	Owner     string `json:"owner"`
	SongCount int    `json:"songCount"`
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
	seen := map[string]bool{}
	var out []mixes.Track

	add := func(songs []song) {
		for _, s := range songs {
			if s.ID == "" || seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			out = append(out, mixes.Track{
				ID:         s.ID,
				Title:      s.Title,
				Artist:     s.Artist,
				Genre:      s.Genre,
				Genres:     s.genreNames(),
				Year:       s.Year,
				PlayCount:  s.PlayCount,
				LastPlayed: parseTime(s.Played),
				Added:      parseTime(s.Created),
				Starred:    s.Starred != "",
			})
		}
	}

	if env, err := c.do("getStarred2", url.Values{}); err == nil {
		add(env.Response.Starred2.Song)
	} else {
		return nil, err
	}

	// Album ids are deduplicated ACROSS the three lists, not just inside each
	// one. The overlap is not marginal: on a server in daily use the same
	// records are both the most played and the most recently played, so the
	// old code spent a full getAlbum round trip on each of them twice. Paying
	// that back is what buys the third list below at roughly the old cost.
	seenAlbum := map[string]bool{}
	for _, listType := range []string{"newest", "frequent", "recent"} {
		env, err := c.do("getAlbumList2", url.Values{
			"type": {listType}, "size": {strconv.Itoa(albumPages)},
		})
		if err != nil {
			return nil, err
		}
		for _, al := range env.Response.AlbumList2.Album {
			if al.ID == "" || seenAlbum[al.ID] {
				continue
			}
			seenAlbum[al.ID] = true
			detail, err := c.do("getAlbum", url.Values{"id": {al.ID}})
			if err != nil {
				// One unreadable album must not abort the whole run.
				continue
			}
			add(detail.Response.Album.Song)
		}
	}

	return out, nil
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
	if c.playlists == nil {
		existing, err := c.Playlists()
		if err != nil {
			return "", err
		}
		c.playlists = existing
	}
	for _, p := range c.playlists {
		if p.Name == name && p.Owner == c.user {
			return p.ID, nil
		}
	}
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
			Logf("could not make %s private: %v", name, err)
		}
	}
	return id, nil
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

// ReplaceTracks sets a playlist's contents to exactly the given ids.
//
// It clears by index from the end backwards. Removing from the front would
// renumber every remaining entry as it went, so the indices in the same
// request would refer to different tracks by the time they were applied.
func (c *Client) ReplaceTracks(playlistID string, current int, trackIDs []string) error {
	params := url.Values{"playlistId": {playlistID}}
	for i := current - 1; i >= 0; i-- {
		params.Add("songIndexToRemove", strconv.Itoa(i))
	}
	for _, id := range trackIDs {
		params.Add("songIdToAdd", id)
	}
	_, err := c.do("updatePlaylist", params)
	return err
}

// SetComment writes the playlist description.
func (c *Client) SetComment(playlistID, comment string) error {
	_, err := c.do("updatePlaylist", url.Values{
		"playlistId": {playlistID}, "comment": {comment},
	})
	return err
}

// TrackCount reports how many tracks a playlist currently holds, which
// ReplaceTracks needs in order to clear it.
func (c *Client) TrackCount(playlistID string) (int, error) {
	env, err := c.do("getPlaylist", url.Values{"id": {playlistID}})
	if err != nil {
		return 0, err
	}
	return env.Response.Playlist.SongCount, nil
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
