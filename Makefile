# Build NaviBeat Mixes into an installable .ndp (a zip of manifest.json + plugin.wasm).
#
# TinyGo is preferred because it produces a much smaller binary, but plain Go
# works: Navidrome's own example Makefile supports both, and the WASM ABI is
# the same either way. See README for the measured size difference.
NDP    := navibeat-mixes.ndp
TINYGO := $(shell command -v tinygo 2> /dev/null)
# The ONE source of truth for the version. Navidrome shows this string in its
# Plugins list, so it is what a user reads back to you in a bug report.
VERSION := $(shell python3 -c "import json;print(json.load(open('manifest.json'))['version'])")

.PHONY: all test clean check-emdash check-version release-tag

all: $(NDP)

plugin.wasm: main.go $(wildcard internal/*/*.go) go.mod
ifdef TINYGO
	tinygo build -target wasip1 -buildmode=c-shared -o $@ .
else
	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o $@ .
endif

$(NDP): plugin.wasm manifest.json
	@rm -f $@
	zip -j $@ manifest.json plugin.wasm

# Unit tests run natively. The PDK ships stub implementations behind
# `//go:build !wasip1` precisely so plugin logic stays testable off-target.
#
# ./internal/... and not ./...: the root package imports the extism PDK, whose
# host bindings exist only for wasip1, so it cannot compile for the host at
# all. Everything worth testing lives under internal/ for exactly that reason.
test:
	go test ./internal/...

# Hard project rule: no em dash anywhere, including code and comments.
#
# Portable on purpose. The old line used `grep -P`, which macOS's BSD grep
# rejects ("invalid option -- P", exit 2); with stderr discarded and the exit
# negated, that error read as a pass, so the check passed on every Mac build
# whatever the tree held (measured 2026-10-21 with a planted em dash). The
# pattern is now the literal UTF-8 bytes, and any grep exit other than
# 0 (found) or 1 (clean) fails the target instead of passing it.
check-emdash:
	@out=$$(grep -rn --exclude-dir=.git "$$(printf '\342\200\224')" .); rc=$$?; \
	if [ $$rc -eq 0 ]; then echo "$$out"; echo "em dash found"; exit 1; fi; \
	if [ $$rc -ne 1 ]; then echo "em dash check could not run (grep exit $$rc)"; exit 1; fi
	@echo "no em dash"

# THE GUARD, and it exists because the two drifted for four releases.
#
# The git tag and the version inside manifest.json are independent strings and
# nothing ever compared them. Measured 2026-08-07:
#
#   tag v0.1.0 -> manifest 0.1.0   ok
#   tag v0.3.0 -> manifest 0.3.0   ok
#   tag v0.4.0 -> manifest 0.3.0   DRIFT
#   tag v0.6.0 -> manifest 0.5.0   DRIFT, and this is the newest release
#
# The consequence is not cosmetic. Navidrome shows the MANIFEST version in its
# Plugins list, so a user on the latest release reads "0.5.0" while the repo
# calls it v0.6.0, and neither of you can tell whether they are up to date. A
# reporter saying "it does not work well at this version" then means nothing.
check-version:
	@test -n "$(VERSION)" || (echo "manifest.json has no version"; exit 1)
	@if git rev-parse "v$(VERSION)" >/dev/null 2>&1; then \
		echo "tag v$(VERSION) already exists; bump manifest.json first"; exit 1; \
	fi
	@echo "manifest version $(VERSION), tag v$(VERSION) is free"

# Tag from the manifest rather than by hand, so they cannot disagree again.
# Pushing the tag and cutting the release stay manual on purpose: publishing is
# a decision, not a build step.
release-tag: check-version test check-emdash $(NDP)
	git tag "v$(VERSION)"
	@echo "tagged v$(VERSION). Now: git push origin v$(VERSION) && gh release create v$(VERSION) $(NDP)"

clean:
	rm -f plugin.wasm $(NDP)
