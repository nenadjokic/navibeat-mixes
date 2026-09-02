package library

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/nenadjokic/navibeat-mixes/internal/mixes"
)

// Pacer is asked after every Subsonic page while the pool is assembled: Step
// marks the page as one unit of work, Spent says whether to stop here and let
// a later call carry on. resume.Budget is the one the plugin passes; nil means
// never stop, which is what CandidatesWith has always done.
type Pacer interface {
	Step()
	Spent() bool
}

// Pool is the candidate pool, possibly partial, in a form small enough to park
// in the kvstore between two host calls.
//
// Found on a live server (2026-09-02): the pool alone, about 300 Subsonic
// calls per user, took longer than the host's 30 second deadline on a loaded
// NAS, so the run was killed before its first playlist write every night for
// a week, and the 20 second budget never got a chance to trip because it was
// only checked between writes. The pool now checks the budget after every
// page, remembers exactly where it stopped, and a continuation picks up from
// there instead of starting over.
//
// The stored form is deliberately not []mixes.Track: the kvstore is capped at
// 1 MB per plugin (Navidrome's defaultMaxKVStoreSize) and the listening
// histories already live there, so every field the builders do not read is
// left out and every date is an integer. Title goes: no builder reads it, and
// it is the longest string a track carries.
type Pool struct {
	// Key names the run this pool belongs to (the day and the options it was
	// assembled with). The caller sets it and refuses a pool whose key does
	// not match, so yesterday's half-built pool never feeds today's mixes.
	Key string `json:"k"`
	// Format is bumped whenever the stored shape changes, so a pool parked by
	// an older build is discarded rather than misread.
	Format int `json:"f"`

	Items []poolTrack `json:"t"`
	// Seen is every album already fetched, across all lists.
	Seen []string `json:"a"`
	// Starred is whether getStarred2 has been folded in yet. It runs first.
	Starred bool `json:"s"`
	// List is the index of the album list being worked on.
	List int `json:"l"`
	// Listed is whether that list's getAlbumList2 has been fetched, in which
	// case Queue holds the albums from it still to fetch.
	Listed bool     `json:"ld"`
	Queue  []string `json:"q"`
	// Complete is set once every list has been folded in.
	Complete bool `json:"c"`
}

// PoolFormat is the current stored shape of a Pool.
const PoolFormat = 1

// poolTrack is one candidate in its stored form. Field names are one or two
// letters because there are thousands of these in one value.
type poolTrack struct {
	ID        string   `json:"i"`
	Artist    string   `json:"ar,omitempty"`
	Genre     string   `json:"g,omitempty"`
	Genres    []string `json:"gs,omitempty"`
	Year      int      `json:"y,omitempty"`
	PlayCount int      `json:"pc,omitempty"`
	// Unix seconds, 0 for never or unknown.
	LastPlayed int64 `json:"lp,omitempty"`
	Added      int64 `json:"ad,omitempty"`
	Released   int64 `json:"rl,omitempty"`
	// Precision is how exact Released is (releaseUnknown to releaseDay), so
	// a later album pass only ever makes it more exact and never less.
	Precision int  `json:"p,omitempty"`
	Starred   bool `json:"st,omitempty"`
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0).UTC()
}

// Tracks hands the pool to the builders in the shape they read.
func (p *Pool) Tracks() []mixes.Track {
	out := make([]mixes.Track, 0, len(p.Items))
	for _, t := range p.Items {
		out = append(out, mixes.Track{
			ID:         t.ID,
			Artist:     t.Artist,
			Genre:      t.Genre,
			Genres:     t.Genres,
			Year:       t.Year,
			PlayCount:  t.PlayCount,
			LastPlayed: fromUnix(t.LastPlayed),
			Added:      fromUnix(t.Added),
			Released:   fromUnix(t.Released),
			Starred:    t.Starred,
		})
	}
	return out
}

// Encode serialises the pool for the kvstore, gzipped.
//
// Measured in the test below: 3000 tracks are about 600 KB as JSON and under
// a tenth of that gzipped. The kvstore is one SQLite row per key, written on
// the same disk the server is scanning, so the smaller write is worth the
// compression time even before the 1 MB cap is considered.
func (p *Pool) Encode() []byte {
	p.Format = PoolFormat
	data, _ := json.Marshal(p)
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

// DecodePool reads a parked pool. Anything unreadable, from another run
// (`key` differs) or from another build (Format differs) comes back as nil,
// so a stale or corrupt value can only ever cost a refetch, never a wrong mix.
func DecodePool(data []byte, key string) *Pool {
	if len(data) == 0 {
		return nil
	}
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil
		}
		if data, err = io.ReadAll(r); err != nil {
			return nil
		}
	}
	var p Pool
	if json.Unmarshal(data, &p) != nil || p.Key != key || p.Format != PoolFormat {
		return nil
	}
	return &p
}

// Albums is how many albums have been fetched so far, for the log line.
func (p *Pool) Albums() int { return len(p.Seen) }

// albumLists is the order the lists are fetched in. `newest` first because it
// is the one that answers on a library nobody has played yet (see Candidates).
func albumLists(opts CandidateOptions) []url.Values {
	lists := []url.Values{
		{"type": {"newest"}},
		{"type": {"frequent"}},
		{"type": {"recent"}},
	}
	// The release-date window, only when the released order asked for it: it
	// costs up to AlbumPages more getAlbum calls, and nobody else uses it.
	//
	// fromYear ABOVE toYear is deliberate and is what makes the list come back
	// newest first: Navidrome swaps the two and sets a descending order when
	// they arrive that way round (server/subsonic/filter/filters.go,
	// AlbumsByYear), sorted on the original date, then the release date.
	if opts.ByReleaseDate && opts.Year > 0 {
		lists = append(lists, url.Values{
			"type":     {"byYear"},
			"fromYear": {strconv.Itoa(opts.Year)},
			"toYear":   {strconv.Itoa(opts.Year - releaseWindowYears)},
		})
	}
	return lists
}

// Assemble builds the candidate pool, or carries on building the one passed
// in, and stops after any page once the pacer says the budget is spent. The
// returned pool is Complete when there is nothing left to fetch; otherwise it
// is exactly what a later call needs to continue from, and the caller parks
// it in the kvstore.
//
// The pool is assembled from the endpoints that are cheap and already biased
// towards music the user cares about: everything starred, then the tracks of
// the newest, most played and most recently played albums. Album ids are
// deduplicated ACROSS the lists (on a server in daily use the same records
// are both the most played and the most recently played), and a song already
// in the pool is not added twice, though its release date may still be
// refined by a later album pass: getStarred2 carries only the song's `year`,
// so a starred track that also sits in a fetched album would otherwise keep
// 1 January while the album knows the day.
func (c *Client) Assemble(opts CandidateOptions, p *Pool, pace Pacer) (*Pool, error) {
	if p == nil {
		p = &Pool{}
	}
	if p.Complete {
		return p, nil
	}
	spent := func() bool {
		if pace == nil {
			return false
		}
		pace.Step()
		return pace.Spent()
	}

	// index maps a track id to its position in p.Items, so a later pass can
	// refine a track that an earlier pass already added.
	index := make(map[string]int, len(p.Items))
	for i, t := range p.Items {
		index[t.ID] = i
	}
	seen := make(map[string]bool, len(p.Seen))
	for _, id := range p.Seen {
		seen[id] = true
	}

	// add folds one list of songs into the pool. `al` is the album they came
	// from, nil for getStarred2, which returns songs without their album.
	add := func(songs []song, al *album) {
		for _, s := range songs {
			if s.ID == "" {
				continue
			}
			released, exact := releasedFrom(al, s)
			if i, ok := index[s.ID]; ok {
				if exact > p.Items[i].Precision {
					p.Items[i].Released = unix(released)
					p.Items[i].Precision = exact
				}
				continue
			}
			index[s.ID] = len(p.Items)
			p.Items = append(p.Items, poolTrack{
				ID:         s.ID,
				Artist:     s.Artist,
				Genre:      s.Genre,
				Genres:     s.genreNames(),
				Year:       s.Year,
				PlayCount:  s.PlayCount,
				LastPlayed: unix(parseTime(s.Played)),
				Added:      unix(parseTime(s.Created)),
				Released:   unix(released),
				Precision:  exact,
				Starred:    s.Starred != "",
			})
		}
	}

	if !p.Starred {
		env, err := c.do("getStarred2", url.Values{})
		if err != nil {
			return p, err
		}
		add(env.Response.Starred2.Song, nil)
		p.Starred = true
		if spent() {
			return p, nil
		}
	}

	lists := albumLists(opts)
	for p.List < len(lists) {
		if !p.Listed {
			params := url.Values{}
			for k, v := range lists[p.List] {
				params[k] = v
			}
			params.Set("size", strconv.Itoa(opts.AlbumPages))
			env, err := c.do("getAlbumList2", params)
			if err != nil {
				return p, err
			}
			p.Queue = p.Queue[:0]
			for _, al := range env.Response.AlbumList2.Album {
				if al.ID != "" && !seen[al.ID] {
					p.Queue = append(p.Queue, al.ID)
				}
			}
			p.Listed = true
			if spent() {
				return p, nil
			}
		}
		for len(p.Queue) > 0 {
			id := p.Queue[0]
			p.Queue = p.Queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			p.Seen = append(p.Seen, id)
			detail, err := c.do("getAlbum", url.Values{"id": {id}})
			if err != nil {
				// One unreadable album must not abort the whole run.
				continue
			}
			add(detail.Response.Album.Song, &detail.Response.Album)
			if spent() {
				return p, nil
			}
		}
		p.List++
		p.Listed = false
		p.Queue = nil
	}
	p.Complete = true
	return p, nil
}
