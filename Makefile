# Build NaviBeat Mixes into an installable .ndp (a zip of manifest.json + plugin.wasm).
#
# TinyGo is preferred because it produces a much smaller binary, but plain Go
# works: Navidrome's own example Makefile supports both, and the WASM ABI is
# the same either way. See README for the measured size difference.
NDP    := navibeat-mixes.ndp
TINYGO := $(shell command -v tinygo 2> /dev/null)

.PHONY: all test clean check-emdash

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
check-emdash:
	@! grep -rnP '\x{2014}' --include='*' . 2>/dev/null || (echo "em dash found"; exit 1)
	@echo "no em dash"

clean:
	rm -f plugin.wasm $(NDP)
