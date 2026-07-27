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
)

// Caller performs a Subsonic request and returns the raw JSON body. Injected
// so this package can be exercised against recorded responses.
type Caller func(uri string) (string, error)

// Client talks to one Subsonic server as one user.
type Client struct {
	call Caller
	user string
}

// New returns a client that acts as the given user. Every Subsonic call needs
// a user to act as, which is why the plugin requires the users permission.
func New(call Caller, user string) *Client {
	return &Client{call: call, user: user}
}

type song struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Genre     string `json:"genre"`
	PlayCount int    `json:"playCount"`
	Played    string `json:"played"`
	Starred   string `json:"starred"`
	Duration  int    `json:"duration"`
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
// starred, plus the tracks of their most played and most recent albums.
//
// There is no "give me every song" call in Subsonic that is safe on a large
// library, so the pool is assembled from the endpoints that are cheap and are
// already biased towards music the user cares about.
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
				PlayCount:  s.PlayCount,
				LastPlayed: parseTime(s.Played),
				Starred:    s.Starred != "",
			})
		}
	}

	if env, err := c.do("getStarred2", url.Values{}); err == nil {
		add(env.Response.Starred2.Song)
	} else {
		return nil, err
	}

	for _, listType := range []string{"frequent", "recent"} {
		env, err := c.do("getAlbumList2", url.Values{
			"type": {listType}, "size": {strconv.Itoa(albumPages)},
		})
		if err != nil {
			return nil, err
		}
		for _, al := range env.Response.AlbumList2.Album {
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
	existing, err := c.Playlists()
	if err != nil {
		return "", err
	}
	for _, p := range existing {
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
	if _, err := c.do("updatePlaylist", url.Values{
		"playlistId": {id}, "public": {"false"},
	}); err != nil {
		// Not fatal: a mix that is visible to others still works, it is just
		// untidy, and failing here would cost the user the whole playlist.
		_ = err
	}
	return id, nil
}

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
