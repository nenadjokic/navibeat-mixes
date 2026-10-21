//go:build !scrobbles

package main

import (
	"time"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

// readPlayHistory in the default build reads nothing, so On Repeat stays
// exactly as it has always been, description included.
//
// WHY THE HISTORY READ IS NOT IN THE DEFAULT BUILD. A wasm import is resolved
// when Navidrome instantiates the plugin, before any of this code runs, and a
// server older than 0.64.0 registers no scrobbleretriever_* host function.
// Measured with the extism go-sdk v1.7.1 and wazero v1.12.0 that Navidrome
// 0.63.2 ships: a module importing scrobbleretriever_getscrobbles fails with
// `"scrobbleretriever_getscrobbles" is not exported in module
// "extism:host/user"`, and Navidrome's loader then disables the plugin. A
// runtime check cannot catch that, so the call lives behind the `scrobbles`
// build tag (history_scrobbles.go) and the default build keeps 0.63.x working.
func readPlayHistory(string, time.Time, time.Duration) *mixes.PlayHistory { return nil }
