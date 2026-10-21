//go:build scrobbles

package main

import (
	"time"

	"github.com/navidrome/navidrome/plugins/pdk/go/host"

	"github.com/nenadjokic/navibeat-mixes/internal/history"
	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

// readPlayHistory reads the user's plays inside the window from Navidrome's
// own scrobble history, through the ScrobbleRetriever host service
// (Navidrome 0.64.0 and newer, manifest permission `scrobbleRetriever`).
//
// ⛔ THIS FILE PUTS scrobbleretriever_getscrobbles INTO THE WASM IMPORTS, and
// a server older than 0.64.0 then refuses to load the plugin at all (see
// history_none.go). A build with this tag is for 0.64.0 and newer only.
//
// Any error (the permission not granted, the user not allowed, a host
// failure) returns it in PlayHistory.Err, and On Repeat falls back to the
// lifetime play count and says so in its description.
func readPlayHistory(username string, now time.Time, window time.Duration) *mixes.PlayHistory {
	from := now.Add(-window).Unix()
	to := now.Unix()
	// Unix SECONDS, both ends inclusive: Navidrome stores submission_time as
	// time.Unix() (persistence/scrobble_repository.go) and filters with >= and
	// <= (plugins/host_scrobbleretriever.go, scrobbleRangeFilters). MaxItems
	// 5000 is the host's ceiling and its default.
	opts := host.ScrobbleOptions{FromTimestamp: &from, ToTimestamp: &to, MaxItems: 5000}
	plays, err := history.Collect(func() ([]mixes.Play, bool, error) {
		refs, next, err := host.ScrobbleRetrieverGetScrobbles(username, opts)
		if err != nil {
			return nil, false, err
		}
		page := make([]mixes.Play, 0, len(refs))
		for _, r := range refs {
			page = append(page, mixes.Play{TrackID: r.MediaFileID, At: time.Unix(r.SubmissionTime, 0)})
		}
		if next == nil {
			return page, false, nil
		}
		// The host's own pagination state, passed back unchanged as its
		// documentation requires.
		opts = *next
		return page, true, nil
	}, history.MaxPages)
	return &mixes.PlayHistory{Plays: plays, Err: err}
}
