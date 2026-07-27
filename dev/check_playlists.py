"""Assert the plugin produced a correct set of mixes.

Reads a Subsonic `getPlaylists` JSON response on stdin. Kept in its own file
rather than a heredoc inside verify.sh, because a heredoc and a pipe both claim
stdin and the script would silently read itself instead of the response.
"""

import json
import sys

EXPECTED_SLOTS = {"morning", "afternoon", "evening", "night", "rediscover"}


def machine_line(comment: str) -> str:
    for line in comment.split("\n"):
        if line.startswith("nb1:"):
            return line
    return ""


def main() -> int:
    resp = json.load(sys.stdin)["subsonic-response"]
    playlists = resp.get("playlists", {}).get("playlist", [])

    # Ours are the ones carrying the marker. Detection keys on the machine
    # line and never on the name, because the prefix is user-configurable.
    ours = [p for p in playlists if machine_line(p.get("comment", ""))]

    failures = []

    if not ours:
        failures.append("no playlist carries an nb1: marker, the plugin produced nothing")

    slots = [machine_line(p["comment"]).split(":")[2] for p in ours]
    missing = EXPECTED_SLOTS - set(slots)
    if missing:
        failures.append(f"missing mixes: {sorted(missing)}")

    # A repeated slot means a run created a second playlist instead of updating
    # the first, which is the orphaned-playlist trap the design set out to
    # avoid and the reason playlist names never embed variable data.
    if len(slots) != len(set(slots)):
        failures.append(f"a slot appeared twice, something duplicated: {sorted(slots)}")

    for p in ours:
        name = p.get("name", "?")
        if p.get("songCount", 0) == 0:
            failures.append(f"{name!r} is empty")

        lines = p.get("comment", "").split("\n")
        if len(lines) != 2:
            failures.append(f"{name!r}: expected a 2 line description, got {len(lines)}")
            continue

        human, machine = lines
        # The human sentence must lead. `playlist.comment` is declared
        # varchar(255) and merely happens not to be enforced today, so if a
        # future release does enforce it, truncation has to eat the machine
        # tail and leave readable text behind.
        if human.startswith("nb1:"):
            failures.append(f"{name!r}: machine line came first, human text must lead")
        if not human.strip():
            failures.append(f"{name!r}: no human description")
        if len(machine.split(":")) != 6:
            failures.append(f"{name!r}: machine line has wrong field count: {machine!r}")

    for f in failures:
        print("FAIL:", f)
    if failures:
        return 1

    print(f"PASS: {len(ours)} mixes, all described, all populated, no duplicates")
    for p in sorted(ours, key=lambda x: x["name"]):
        print(f"       {p['name']}  {p['songCount']:>3} tracks  |  {machine_line(p['comment'])}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
