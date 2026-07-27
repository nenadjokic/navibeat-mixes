"""Assert the plugin left exactly one correctly described playlist behind.

Reads a Subsonic `getPlaylists` JSON response on stdin. Kept in its own file
rather than a heredoc inside verify.sh, because a heredoc and a pipe both claim
stdin and the script would silently read itself instead of the response.
"""

import json
import os
import sys

WANT_NAME = os.environ.get("WANT_NAME", "NaviBeat Mixes toolchain check")


def main() -> int:
    resp = json.load(sys.stdin)["subsonic-response"]
    playlists = resp.get("playlists", {}).get("playlist", [])
    ours = [p for p in playlists if p.get("name") == WANT_NAME]

    failures = []

    if len(ours) != 1:
        failures.append(
            f"expected exactly 1 playlist named {WANT_NAME!r}, found {len(ours)}"
        )
    else:
        comment = ours[0].get("comment", "")
        lines = comment.split("\n")
        if len(lines) != 2:
            failures.append(f"expected a 2 line description, got {len(lines)}: {comment!r}")
        else:
            human, machine = lines
            # The human sentence must lead. `playlist.comment` is declared
            # varchar(255) and merely happens not to be enforced, so if a
            # future release does enforce it, truncation has to eat the
            # machine tail and leave readable text behind.
            if not machine.startswith("nb1:"):
                failures.append(f"second line is not the machine line: {machine!r}")
            if human.startswith("nb1:"):
                failures.append("machine line came first, human text must lead")
            if len(machine.split(":")) != 6:
                failures.append(f"machine line has wrong field count: {machine!r}")

    for f in failures:
        print("FAIL:", f)
    if failures:
        return 1

    print("PASS: exactly one playlist, description round tripped intact")
    print("      ", repr(ours[0]["comment"]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
