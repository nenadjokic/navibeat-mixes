package library

import (
	"testing"

	"github.com/nenadjokic/navibeat-mixes/internal/protocol"
)

// The mailbox as a client actually writes it: human text plus the machine line.
func controlComment() string {
	return protocol.Format("NaviBeat writes commands here.", protocol.Meta{Kind: "control"})
}

// The whole reason FindControl exists. On an ADMIN account Navidrome applies no
// filter at all to getPlaylists, so the list carries every user's playlists,
// private ones included. Matching by name alone handed the admin whichever
// mailbox came first.
func TestFindControlNeverReturnsAnotherUsersMailbox(t *testing.T) {
	name := "🟠 NaviBeat Control"
	playlists := []Playlist{
		{ID: "1", Name: name, Owner: "alice", Comment: controlComment()},
		{ID: "2", Name: name, Owner: "bob", Comment: controlComment()},
	}

	got := FindControl(playlists, name, "bob")
	if got == nil {
		t.Fatal("bob's own mailbox was not found")
	}
	if got.ID != "2" {
		t.Fatalf("returned playlist %s owned by %s, wanted bob's", got.ID, got.Owner)
	}
}

// The machine-line fallback was the worse of the two loops: it ignored the name
// entirely, so it matched the first control playlist ANYWHERE on the server.
func TestFindControlFallbackIsAlsoOwnerFiltered(t *testing.T) {
	playlists := []Playlist{
		{ID: "1", Name: "Renamed by alice", Owner: "alice", Comment: controlComment()},
		{ID: "2", Name: "Renamed by bob", Owner: "bob", Comment: controlComment()},
	}

	got := FindControl(playlists, "🟠 NaviBeat Control", "bob")
	if got == nil {
		t.Fatal("the machine line should still find a renamed mailbox")
	}
	if got.ID != "2" {
		t.Fatalf("fallback returned %s owned by %s, wanted bob's", got.ID, got.Owner)
	}
}

// A user who has no mailbox must get nothing rather than somebody else's. The
// caller reads a nonce out of this playlist and executes the command in it, so
// a wrong answer here is worse than no answer.
func TestFindControlReturnsNilWhenTheUserHasNoMailbox(t *testing.T) {
	playlists := []Playlist{
		{ID: "1", Name: "🟠 NaviBeat Control", Owner: "alice", Comment: controlComment()},
	}

	if got := FindControl(playlists, "🟠 NaviBeat Control", "carol"); got != nil {
		t.Fatalf("carol was handed playlist %s owned by %s", got.ID, got.Owner)
	}
}

// The name match wins over the machine line, because a user who renamed one
// playlist and left an old one behind means the configured name.
func TestFindControlPrefersTheConfiguredName(t *testing.T) {
	playlists := []Playlist{
		{ID: "old", Name: "leftover", Owner: "bob", Comment: controlComment()},
		{ID: "new", Name: "🟠 NaviBeat Control", Owner: "bob", Comment: controlComment()},
	}

	got := FindControl(playlists, "🟠 NaviBeat Control", "bob")
	if got == nil || got.ID != "new" {
		t.Fatalf("wanted the named mailbox, got %+v", got)
	}
}

// An ordinary mix must never be mistaken for a mailbox, or the plugin would
// start executing commands out of its own Morning playlist.
func TestFindControlIgnoresOrdinaryMixes(t *testing.T) {
	playlists := []Playlist{
		{ID: "1", Name: "🟠 Morning", Owner: "bob",
			Comment: protocol.Format("Your mornings.", protocol.Meta{Kind: "timeofday", Slot: "morning"})},
	}

	if got := FindControl(playlists, "🟠 NaviBeat Control", "bob"); got != nil {
		t.Fatalf("a timeofday mix was treated as a mailbox: %+v", got)
	}
}
