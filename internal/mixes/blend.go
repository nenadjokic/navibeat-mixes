package mixes

import (
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Blend (NaviBeat #503551): one playlist for two people who share a server,
// built from both of their listening histories.
//
// THE HISTORY IS THE COLLECTOR'S, per user (State.TotalPlays): what this plugin
// watched each account play since it was installed. It is the only per-user,
// per-track play record the default build has, and it reads the same for any
// account, so the plugin needs no permission beyond the ones it already has.
//
// THE ORDER: songs both people play first (by the smaller of the two play
// counts, so a song one of them played once does not lead), then one song from
// each person's own favourites in turn, owner first. Every candidate must be in
// the OWNER's pool (the tracks this account can see), because the copy is
// written into the owner's library and a track they cannot see would fail the
// write or leak another library's music.
//
// THE MATCH: the share of the lighter listener's played songs that the other
// has played too, rounded to a whole percent. It reads as "how much of your
// taste the other person shares" and does not punish the person with the
// smaller library the way a plain overlap of both sets would.

// BlendMinPlayed is how many different songs each person must have played
// before a Blend is written. Under it the match percentage is noise.
const BlendMinPlayed = 10

// BlendInput is everything one Blend is built from.
type BlendInput struct {
	// Owner is the account the playlist is written for; Partner the other one.
	Owner, Partner string
	// OwnerPlays and PartnerPlays map a track id to observed plays.
	OwnerPlays, PartnerPlays map[string]int
	// Pool is the OWNER's candidate tracks; nothing outside it is selected.
	Pool         []Track
	Size         int
	MaxPerArtist int
}

// BlendResult is the selection plus the two numbers the blend line carries.
type BlendResult struct {
	Selection Selection
	// Match is 0..100.
	Match int
	// Shared counts songs both people played, whether or not the owner can
	// see them, because it describes the two people and not the playlist.
	Shared int
	// Enough is false when either person has played fewer than
	// BlendMinPlayed songs; the caller then writes nothing.
	Enough bool
}

// BlendSlot is the slot for a pair: colon free, the same for both owners, and
// stable while the pair stays configured.
func BlendSlot(a, b string) Slot {
	x, y := strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if y < x {
		x, y = y, x
	}
	h := fnv.New32a()
	h.Write([]byte(x + "\n" + y))
	return Slot("blend-" + strconv.FormatUint(uint64(h.Sum32()), 16))
}

func played(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for id, n := range m {
		if n > 0 && id != "" {
			out[id] = n
		}
	}
	return out
}

// BlendMatch is the match percentage and the shared count for two histories.
func BlendMatch(a, b map[string]int) (match, shared int) {
	pa, pb := played(a), played(b)
	for id := range pa {
		if _, ok := pb[id]; ok {
			shared++
		}
	}
	lighter := len(pa)
	if len(pb) < lighter {
		lighter = len(pb)
	}
	if lighter == 0 {
		return 0, shared
	}
	return int(math.Round(100 * float64(shared) / float64(lighter))), shared
}

// BuildBlend selects the tracks for one owner's copy of a Blend.
func BuildBlend(in BlendInput) BlendResult {
	owner, partner := played(in.OwnerPlays), played(in.PartnerPlays)
	match, shared := BlendMatch(owner, partner)
	res := BlendResult{Match: match, Shared: shared,
		Enough: len(owner) >= BlendMinPlayed && len(partner) >= BlendMinPlayed}
	res.Selection = Selection{Slot: BlendSlot(in.Owner, in.Partner), Mode: ModeAffinity}
	if !res.Enough || in.Size <= 0 {
		return res
	}

	byID := make(map[string]Track, len(in.Pool))
	for _, t := range in.Pool {
		byID[t.ID] = t
	}
	type cand struct {
		id      string
		primary int
		total   int
	}
	var both, mine, theirs []cand
	for id, n := range owner {
		if _, ok := byID[id]; !ok {
			continue
		}
		if m, ok := partner[id]; ok {
			lo := n
			if m < lo {
				lo = m
			}
			both = append(both, cand{id, lo, n + m})
		} else {
			mine = append(mine, cand{id, n, n})
		}
	}
	for id, n := range partner {
		if _, ok := byID[id]; !ok {
			continue
		}
		if _, ok := owner[id]; !ok {
			theirs = append(theirs, cand{id, n, n})
		}
	}
	order := func(c []cand) {
		sort.Slice(c, func(i, j int) bool {
			if c[i].primary != c[j].primary {
				return c[i].primary > c[j].primary
			}
			if c[i].total != c[j].total {
				return c[i].total > c[j].total
			}
			return c[i].id < c[j].id
		})
	}
	order(both)
	order(mine)
	order(theirs)

	perArtist := map[string]int{}
	var ids []string
	take := func(c cand) bool {
		if len(ids) >= in.Size {
			return false
		}
		artist := strings.ToLower(byID[c.id].Artist)
		if in.MaxPerArtist > 0 && artist != "" && perArtist[artist] >= in.MaxPerArtist {
			return false
		}
		perArtist[artist]++
		ids = append(ids, c.id)
		return true
	}
	for _, c := range both {
		take(c)
	}
	i, j := 0, 0
	for len(ids) < in.Size && (i < len(mine) || j < len(theirs)) {
		for i < len(mine) {
			c := mine[i]
			i++
			if take(c) {
				break
			}
		}
		for j < len(theirs) && len(ids) < in.Size {
			c := theirs[j]
			j++
			if take(c) {
				break
			}
		}
	}
	res.Selection.TrackIDs = ids
	return res
}
