#!/usr/bin/env bash
# Start a scratch Navidrome for plugin development and install the built .ndp.
#
# This is a throwaway instance. It is never a real music server: the port is
# 4544 rather than the default 4533 specifically so a mistyped command cannot
# reach one, and the admin password is a well known development value.
#
# Usage:
#   make                # build navibeat-mixes.ndp first
#   dev/up.sh           # start the container and install the plugin
#   dev/verify.sh       # assert the plugin did its job
set -euo pipefail

cd "$(dirname "$0")/.."

NAME="nd-dev"
PORT="${ND_PORT:-4544}"
# Pinned on purpose: the plugin ABI has to match the host it is built against,
# and the PDK version in go.mod is pinned to this same release.
IMAGE="ghcr.io/navidrome/navidrome:0.63.1"
NDP="navibeat-mixes.ndp"

if [[ ! -f "$NDP" ]]; then
  echo "$NDP not found, run 'make' first" >&2
  exit 1
fi

mkdir -p dev/music dev/data dev/plugins
cp "$NDP" dev/plugins/

# A library with nothing in it makes every mix degenerate, so drop in one
# generated track if ffmpeg is around. Real development wants a few hundred
# tracks across several genres.
if [[ ! -f dev/music/proof.mp3 ]] && command -v ffmpeg >/dev/null; then
  ffmpeg -f lavfi -i anullsrc=r=44100:cl=mono -t 2 \
    -metadata title="Proof Track" -metadata artist="Proof Artist" \
    -metadata album="Proof Album" -y dev/music/proof.mp3 -loglevel error
fi

docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" -p "$PORT":4533 \
  -v "$PWD/dev/music":/music:ro \
  -v "$PWD/dev/data":/data \
  -v "$PWD/dev/plugins":/plugins \
  -e ND_LOGLEVEL=debug \
  -e ND_PLUGINS_ENABLED=true \
  -e ND_PLUGINS_FOLDER=/plugins \
  -e ND_DEVAUTOCREATEADMINPASSWORD=devpassword \
  "$IMAGE" >/dev/null

echo "waiting for first start ..."
sleep 20

# Navidrome discovers a plugin but leaves it disabled until someone approves
# its permissions, which normally happens in the admin UI. For an unattended
# dev loop we grant it directly. The container is stopped first because writing
# to a live SQLite database is how you get a corrupted one.
docker stop "$NAME" >/dev/null
sleep 2
sqlite3 dev/data/navidrome.db \
  "update plugin set enabled=1, all_users=1, all_libraries=1 where id='navibeat-mixes';"
docker start "$NAME" >/dev/null
sleep 25

echo "scratch Navidrome on http://localhost:$PORT  (admin / devpassword)"
docker logs "$NAME" 2>&1 | grep -iE "Loaded plugin|plugin.*error" | tail -5 || true
