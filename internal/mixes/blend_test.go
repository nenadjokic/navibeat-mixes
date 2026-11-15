package mixes

import (
	"strconv"
	"testing"
)

func plays(prefix string, n, each int) map[string]int {
	m := map[string]int{}
	for i := 0; i < n; i++ {
		m[prefix+strconv.Itoa(i)] = each
	}
	return m
}

func poolOf(ids ...map[string]int) []Track {
	seen := map[string]bool{}
	var out []Track
	for _, m := range ids {
		for id := range m {
			if !seen[id] {
				seen[id] = true
				out = append(out, Track{ID: id, Artist: "artist-" + id})
			}
		}
	}
	return out
}

func TestBlendMatchIsTheShareOfTheLighterListener(t *testing.T) {
	a := plays("s", 10, 1) // s0..s9
	b := plays("s", 4, 2)  // s0..s3, all shared
	for i := 0; i < 4; i++ {
		b["b"+strconv.Itoa(i)] = 1
	} // b plays 8 songs, 4 of them shared
	match, shared := BlendMatch(a, b)
	if shared != 4 || match != 50 {
		t.Fatalf("match %d shared %d, want 50 and 4", match, shared)
	}
	if m, s := BlendMatch(nil, b); m != 0 || s != 0 {
		t.Fatalf("an empty history must match 0, got %d %d", m, s)
	}
}

func TestBlendPutsSharedSongsFirstThenAlternates(t *testing.T) {
	owner := map[string]int{"both1": 5, "both2": 2, "mine1": 9, "mine2": 3}
	partner := map[string]int{"both1": 1, "both2": 7, "theirs1": 8, "theirs2": 4}
	for i := 0; i < 10; i++ { // enough history on both sides
		owner["o"+strconv.Itoa(i)] = 1
		partner["p"+strconv.Itoa(i)] = 1
	}
	res := BuildBlend(BlendInput{Owner: "nenad", Partner: "ana", OwnerPlays: owner, PartnerPlays: partner,
		Pool: poolOf(owner, partner), Size: 6})
	got := res.Selection.TrackIDs
	want := []string{"both2", "both1", "mine1", "theirs1", "mine2", "theirs2"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if !res.Enough || res.Shared != 2 {
		t.Fatalf("enough %v shared %d", res.Enough, res.Shared)
	}
}

func TestBlendNeverPicksATrackTheOwnerCannotSee(t *testing.T) {
	owner := plays("o", 12, 1)
	partner := plays("p", 12, 3)
	pool := poolOf(owner) // the partner's songs live in a library the owner cannot see
	res := BuildBlend(BlendInput{Owner: "nenad", Partner: "ana", OwnerPlays: owner, PartnerPlays: partner,
		Pool: pool, Size: 30})
	for _, id := range res.Selection.TrackIDs {
		if id[0] == 'p' {
			t.Fatalf("picked %s outside the owner's pool", id)
		}
	}
}

func TestBlendWithTooLittleHistoryWritesNothing(t *testing.T) {
	owner := plays("o", BlendMinPlayed-1, 1)
	partner := plays("p", 40, 1)
	res := BuildBlend(BlendInput{Owner: "a", Partner: "b", OwnerPlays: owner, PartnerPlays: partner,
		Pool: poolOf(owner, partner), Size: 30})
	if res.Enough || len(res.Selection.TrackIDs) != 0 {
		t.Fatalf("enough %v, %d tracks", res.Enough, len(res.Selection.TrackIDs))
	}
}

func TestBlendSlotIsTheSameForBothOwnersAndColonFree(t *testing.T) {
	a, b := BlendSlot("Nenad", "ana"), BlendSlot("ana", "nenad")
	if a != b {
		t.Fatalf("%s != %s", a, b)
	}
	for _, r := range string(a) {
		if r == ':' {
			t.Fatalf("slot %q has a colon", a)
		}
	}
	if BlendSlot("nenad", "marko") == a {
		t.Fatal("two pairs share a slot")
	}
}

func TestBlendRespectsTheArtistCap(t *testing.T) {
	owner := map[string]int{}
	partner := map[string]int{}
	var pool []Track
	for i := 0; i < 12; i++ {
		id := "x" + strconv.Itoa(i)
		owner[id] = 2
		partner[id] = 2
		pool = append(pool, Track{ID: id, Artist: "Same Band"})
	}
	res := BuildBlend(BlendInput{Owner: "a", Partner: "b", OwnerPlays: owner, PartnerPlays: partner,
		Pool: pool, Size: 30, MaxPerArtist: 3})
	if len(res.Selection.TrackIDs) != 3 {
		t.Fatalf("got %d tracks from one artist", len(res.Selection.TrackIDs))
	}
}
