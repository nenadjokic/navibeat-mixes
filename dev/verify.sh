#!/usr/bin/env bash
# Integration check for the NaviBeat Mixes plugin against a scratch Navidrome.
#
# This is the assertion that unit tests cannot make: that the plugin actually
# loads into a real host, is granted its permissions, reaches the Subsonic API,
# and leaves exactly one playlist behind whose description survived the round
# trip intact.
#
# It NEVER touches a production server. It talks only to the local scratch
# container started by `dev/up.sh`.
#
# Usage:  dev/verify.sh
set -euo pipefail

HOST="${ND_HOST:-http://localhost:4544}"
USER="${ND_USER:-admin}"
PASS="${ND_PASS:-devpassword}"
WANT_NAME="NaviBeat Mixes toolchain check"

if [[ "$HOST" == *"192.168."* ]]; then
  echo "refusing to run against what looks like a real server: $HOST" >&2
  exit 2
fi

# $1 is the endpoint name. Auth and format params are appended here so no
# caller has to remember them, and so credentials appear in exactly one place.
api() {
  curl -sS "$HOST/rest/$1?u=$USER&p=$PASS&v=1.16.1&c=navibeat-mixes-verify&f=json"
}

echo "server: $(api ping | python3 -c 'import json,sys; print(json.load(sys.stdin)["subsonic-response"]["serverVersion"])')"

api getPlaylists | WANT_NAME="$WANT_NAME" python3 "$(dirname "$0")/check_playlists.py"
