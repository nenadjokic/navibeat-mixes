// NaviBeat Mixes: a Navidrome plugin that generates listening-aware playlists.
//
// This file is currently the TOOLCHAIN PROOF, not the product. It does the
// smallest thing that exercises every moving part end to end: it loads into
// Navidrome, resolves a user, calls the Subsonic API, and creates exactly one
// playlist with a description in the wire format the client will later read.
// Product logic (mix selection, the scrobble histogram, the control mailbox)
// lands on top of a path that is already known to work.
//
// Build:
//
//	make            # produces navibeat-mixes.ndp
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/navidrome/navidrome/plugins/pdk/go/host"
	"github.com/navidrome/navidrome/plugins/pdk/go/lifecycle"
	"github.com/navidrome/navidrome/plugins/pdk/go/scrobbler"

	"github.com/extism/go-pdk"

	"github.com/nenadjokic/navibeat-mixes/internal/protocol"
)

// playlistName is FIXED and never embeds variable data such as an artist or a
// date. Embedding variable data is what made another playlist plugin leak a
// fresh orphaned playlist every time its rankings shifted, which then needed a
// cleanup job. Identity here is the slot, never the content.
const playlistName = "NaviBeat Mixes toolchain check"

func init() {
	lifecycle.Register(&plugin{})
	// The manifest says NOTHING about capabilities, on purpose. If the host
	// reports Scrobbler after this line is added, capability really is decided
	// by which functions the WASM exports, which is the assumption the whole
	// time-of-day feature rests on.
	scrobbler.Register(&plugin{})
}

type plugin struct{}

var _ lifecycle.InitProvider = (*plugin)(nil)

// OnInit runs when Navidrome loads the plugin.
func (p *plugin) OnInit() error {
	username, err := firstUsername()
	if err != nil {
		return err
	}
	return ensurePlaylist(username)
}

// firstUsername resolves a user to act as. The Subsonic host service requires
// a `u=` parameter on every call, which is the whole reason this plugin needs
// the `users` permission. Admins are preferred because a playlist has to be
// owned by somebody and an admin is guaranteed to exist.
func firstUsername() (string, error) {
	admins, err := host.UsersGetAdmins()
	if err == nil && len(admins) > 0 {
		return admins[0].UserName, nil
	}
	users, err := host.UsersGetUsers()
	if err != nil {
		return "", fmt.Errorf("resolving a user: %w", err)
	}
	if len(users) == 0 {
		return "", errors.New("no users on this server, cannot own a playlist")
	}
	return users[0].UserName, nil
}

// subsonicResponse is the sliver of the Subsonic envelope this proof needs.
// Navidrome answers HTTP 200 with status "failed" inside the body, so the
// error has to be read out of the JSON rather than inferred from the call
// returning without error.
type subsonicResponse struct {
	SubsonicResponse struct {
		Status string `json:"status"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Playlists struct {
			Playlist []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"playlist"`
		} `json:"playlists"`
		Playlist struct {
			ID string `json:"id"`
		} `json:"playlist"`
	} `json:"subsonic-response"`
}

func call(uri string) (*subsonicResponse, error) {
	raw, err := host.SubsonicAPICall(uri)
	if err != nil {
		return nil, fmt.Errorf("subsonic call %q: %w", uri, err)
	}
	var out subsonicResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decoding response to %q: %w", uri, err)
	}
	if out.SubsonicResponse.Status != "ok" {
		if e := out.SubsonicResponse.Error; e != nil {
			return nil, fmt.Errorf("subsonic %q failed: %d %s", uri, e.Code, e.Message)
		}
		return nil, fmt.Errorf("subsonic %q failed with no error detail", uri)
	}
	return &out, nil
}

// ensurePlaylist creates the playlist once and updates it on every later run.
// It is deliberately idempotent: OnInit fires on every server start, and a
// plugin that created a duplicate each time would reproduce exactly the
// orphaned-playlist problem this design set out to avoid.
func ensurePlaylist(username string) error {
	id, err := findPlaylist(username)
	if err != nil {
		return err
	}
	if id == "" {
		created, err := call("createPlaylist?name=" + url.QueryEscape(playlistName) + "&u=" + url.QueryEscape(username))
		if err != nil {
			return err
		}
		id = created.SubsonicResponse.Playlist.ID
	}
	if id == "" {
		return errors.New("createPlaylist returned no playlist id")
	}
	return describe(id, username)
}

func findPlaylist(username string) (string, error) {
	resp, err := call("getPlaylists?u=" + url.QueryEscape(username))
	if err != nil {
		return "", err
	}
	for _, pl := range resp.SubsonicResponse.Playlists.Playlist {
		if pl.Name == playlistName {
			return pl.ID, nil
		}
	}
	return "", nil
}

// describe writes the description in the wire format, which is the part worth
// proving: it confirms a plugin can put a multi-line comment on a playlist and
// that the machine line survives the round trip through the server.
func describe(id, username string) error {
	comment := protocol.Format(
		"Toolchain check for the NaviBeat Mixes plugin. Safe to delete.",
		protocol.Meta{
			Kind:  "diagnostic",
			Slot:  "toolchain",
			Date:  time.Now().Format("2006-01-02"),
			Mode:  "fallback",
			Count: 0,
		},
	)
	_, err := call("updatePlaylist?playlistId=" + url.QueryEscape(id) +
		"&comment=" + url.QueryEscape(comment) +
		"&u=" + url.QueryEscape(username))
	return err
}

// The four Scrobbler methods below are all required: the PDK registers the
// capability as a set, and a missing one would make the host skip the plugin.
// Only Scrobble carries the signal the Collector will eventually want. The
// rest are honest no-ops for now, and each logs so the host's real call
// pattern for a single play can be observed rather than guessed at.

func (p *plugin) IsAuthorized(req scrobbler.IsAuthorizedRequest) (bool, error) {
	pluginLog("scrobbler.IsAuthorized user=" + req.Username)
	return true, nil
}

func (p *plugin) NowPlaying(req scrobbler.NowPlayingRequest) error {
	pluginLog("scrobbler.NowPlaying user=" + req.Username + " track=" + req.Track.Title)
	return nil
}

func (p *plugin) Scrobble(req scrobbler.ScrobbleRequest) error {
	pluginLog("scrobbler.Scrobble user=" + req.Username + " track=" + req.Track.Title +
		" ts=" + strconv.FormatInt(req.Timestamp, 10))
	return nil
}

func (p *plugin) PlaybackReport(req scrobbler.PlaybackReportRequest) error {
	pluginLog("scrobbler.PlaybackReport user=" + req.Username + " state=" + req.State +
		" track=" + req.Track.Title)
	return nil
}

// pluginLog writes where the host will show it. A plugin has no stdout of its
// own worth reading, so this is how the call pattern gets observed.
func pluginLog(msg string) {
	pdk.Log(pdk.LogInfo, "[navibeat-mixes] "+msg)
}

func main() {}
