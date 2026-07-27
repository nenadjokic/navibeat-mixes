# NaviBeat Mixes

A Navidrome plugin that builds listening-aware playlists: time-of-day mixes, a
Rediscover mix, and Wrapped style recaps.

Because it creates ordinary playlists through the Subsonic API, the results
show up in **every** client you use: the Navidrome web UI, Symfonium, Amperfy,
play:Sub, NaviBeat, anything else. There is nothing to install on the client
side and no client is treated as a second class citizen.

> **Status: early.** What exists today is the build and load path, proven end to
> end against Navidrome 0.63.1. The mixes themselves are next.

## Requirements

- Navidrome **0.63.1 or later**, with plugins enabled
- Go 1.25+ to build (TinyGo optional, see below)

## Build

```bash
make          # produces navibeat-mixes.ndp
make test     # unit tests, run natively
```

Navidrome's own example Makefile supports two toolchains and so does this one:

| Toolchain | Command | Size of this plugin |
|---|---|---|
| Standard Go | `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared` | 3.5 MB |
| TinyGo | `tinygo build -target wasip1 -buildmode=c-shared` | smaller, not yet measured here |

The Makefile picks TinyGo automatically when it is on your PATH and falls back
to standard Go otherwise. Standard Go is what the current build is verified
with. TinyGo is worth installing before publishing a release, because the
binary ships to every user who installs the plugin.

## Install

1. Copy `navibeat-mixes.ndp` into your Navidrome plugins folder.
2. Enable plugins in `navidrome.toml`:

   ```toml
   [Plugins]
   Enabled = true
   ```

3. Approve the plugin in Navidrome. It asks for:

   | Permission | Why |
   |---|---|
   | `subsonicapi` | To read your library and create playlists |
   | `users` | Every Subsonic call needs a user to act as |

   Nothing leaves your server. There is no network permission and no telemetry.

## Development

You need a scratch Navidrome. **Never point this at a server you care about.**

```bash
make          # build the .ndp
dev/up.sh     # start a throwaway Navidrome on port 4544 and install the plugin
dev/verify.sh # assert the plugin loaded and did its job
```

`dev/up.sh` pins the image to the same release the PDK in `go.mod` is pinned
to, because a plugin has to match the host ABI it was built against.

A fresh Navidrome has no listening history, so anything that selects on play
counts or recency has nothing to work with out of the box. Seed the scratch
database directly, with the container stopped:

```sql
INSERT INTO annotation (user_id, item_id, item_type, play_count, play_date, starred, starred_at)
SELECT '<user-id>', id, 'media_file', 5, datetime('now','-8 months'), 1, datetime('now','-8 months')
FROM media_file LIMIT 40;
```

That produces starred tracks last played eight months ago, which is exactly the
Rediscover case. Vary `play_count` and `play_date` across rows to exercise the
thresholds.

## How it talks to clients

A Navidrome plugin cannot serve HTTP, register a route, or draw any UI. The
only surface it has is the playlists it creates, so the playlist description
carries both the human explanation and a single compact machine line:

```
Morning mix. Picks from what you play before 11am. Refreshes daily.
nb1:timeofday:morning:2026-07-27:affinity:30
```

Clients that know nothing about this show one short extra line, which is
harmless. Clients that do understand it can render something richer.

The human sentence comes first deliberately. `playlist.comment` is declared
`varchar(255)` and simply is not enforced today. If that ever changes,
truncation removes the machine tail and leaves a description a person can still
read.

## Licence

TBD before first public release.
