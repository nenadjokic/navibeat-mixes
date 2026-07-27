"""Capture README screenshots from the scratch Navidrome.

Run after dev/up.sh and a generation pass, so the mixes actually exist:

    python3 dev/shots.py

Writes into docs/img/. Deliberately points at localhost only: these are
pictures of a throwaway instance with a generated sample library, never of
anyone's real collection.
"""

import pathlib
import sys

from playwright.sync_api import sync_playwright

BASE = "http://localhost:4544"
USER = "admin"
PASSWORD = "devpassword"
OUT = pathlib.Path(__file__).resolve().parent.parent / "docs" / "img"


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)

    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page(viewport={"width": 1280, "height": 860})

        page.goto(f"{BASE}/app/", wait_until="networkidle")

        # The web UI keeps a session, so log in once if the form is showing.
        if page.locator("input[name='username']").count():
            page.fill("input[name='username']", USER)
            page.fill("input[name='password']", PASSWORD)
            page.keyboard.press("Enter")
            page.wait_for_load_state("networkidle")

        page.wait_for_timeout(2500)
        page.screenshot(path=str(OUT / "playlists.png"))
        print("wrote", OUT / "playlists.png")

        # The description is the whole client-facing contract, so it gets a
        # picture of its own showing it reads as ordinary human text in a
        # client that knows nothing about this plugin.
        for name in ("Rediscover", "Morning"):
            link = page.get_by_text(name, exact=False).first
            if not link.count():
                continue
            link.click()
            page.wait_for_load_state("networkidle")
            page.wait_for_timeout(2500)
            out = OUT / f"mix-{name.lower()}.png"
            page.screenshot(path=str(out))
            print("wrote", out)

        browser.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
