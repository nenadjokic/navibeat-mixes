// Package history reads the server's own play history page by page.
//
// The host call itself lives in the root package (only it can import the
// wasip1 host bindings); this package holds the paging rule so it can be
// tested natively.
package history

import (
	"fmt"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

// MaxPages bounds one read. Navidrome returns at most 5000 scrobbles a page
// (plugins/host/scrobble_retriever.go), so this is 100000 plays in 30 days,
// far past any real listener, and a server that never stops saying "more"
// cannot hold the run.
const MaxPages = 20

// Fetch returns the next page and whether another one follows.
type Fetch func() (plays []mixes.Play, more bool, err error)

// Collect calls fetch until the server says there is no next page. Any error,
// including running into the page limit, returns no plays at all: a half
// read history must not be ranked as if it were whole, and the caller falls
// back to the lifetime play count instead.
func Collect(fetch Fetch, maxPages int) ([]mixes.Play, error) {
	var all []mixes.Play
	for page := 0; page < maxPages; page++ {
		plays, more, err := fetch()
		if err != nil {
			return nil, err
		}
		all = append(all, plays...)
		if !more {
			return all, nil
		}
	}
	return nil, fmt.Errorf("play history longer than %d pages", maxPages)
}
