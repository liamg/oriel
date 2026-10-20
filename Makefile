MODULES := $(shell go list -m -f '{{.Path}}/...')
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: test test-vt vet helper build run app dmg native oracle check fmt-check depcheck refcheck vendor-check vulncheck

GOARCH ?= $(shell go env GOARCH)
VERSION ?= dev
HELPER := sandbox/helper/bin/oriel-sandbox-linux-$(GOARCH)
# RELEASE_KEYS (hex ed25519 public keys, comma-separated) gives the build its updater; release.yml sets it.
LDFLAGS := -X github.com/liamg/oriel/cmd/oriel/cli.version=$(VERSION) $(if $(RELEASE_KEYS),-X github.com/liamg/oriel/update.releaseKeys=$(RELEASE_KEYS))
# test runs the timing budgets (tests named …Budget, perf.Timed) after the rest, one package at a time, so other
# packages' tests running alongside can't fail them.
test:
	ORIEL_BUDGETS=later go test $(MODULES)
	go test -p 1 -run 'Budget$$' $(MODULES)

# test-vt runs the terminal emulator's suite on both backends; it needs native first.
test-vt:
	go test -tags ghostty_native ./term/...

oracle:
	ORIEL_ORACLE=1 go test ./vim/...

vet:
	go vet $(MODULES)

# helper is the sandbox helper the app embeds (sandbox/helper): cmd/oriel-sandbox, static, for Linux on the app's
# arch, which docker sandboxes run as their oriel.
helper:
	cd sandbox && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -buildvcs=false -ldflags=-s -o ../$(HELPER) ./cmd/oriel-sandbox
	gzip -9nf $(HELPER)

# build is the desktop app, on the native libghostty-vt, with its sandbox helper; it needs native first. Without the
# tag (or cgo) the emulator runs as wasm. GOARCH=amd64 on an Apple-silicon Mac builds the Intel app.
build: helper
	@test -f term/ghostty/native/include/ghostty/vt.h && test -f term/ghostty/native/lib/$(shell go env GOOS)_$(GOARCH)/libghostty-vt.a || \
		{ echo "make build: libghostty-vt isn't built for $(shell go env GOOS)_$(GOARCH): run make native first (it needs zig 0.16.0)" >&2; exit 1; }
	go build -tags ghostty_native -ldflags "$(LDFLAGS)" -o bin/oriel ./cmd/oriel

# run builds the app and runs it: make run ARGS=~/src/project
run: build
	./bin/oriel $(ARGS)

# app is bin/Oriel.app, signed with CODESIGN_IDENTITY (ad hoc by default; release.yml uses the self-signed one), and
# dmg packs it for first install (distribution.md). macOS only.
CODESIGN_IDENTITY ?= -
APP := bin/Oriel.app
ICNS := bin/oriel.icns
DMG := bin/Oriel-$(patsubst v%,%,$(VERSION))-$(GOARCH).dmg
ifeq ($(shell uname -s),Darwin)
app: build $(ICNS)
	scripts/macos-app.sh $(APP) bin/oriel $(ICNS) "$(VERSION)" "$(CODESIGN_IDENTITY)"

dmg: app
	scripts/macos-dmg.sh $(APP) $(DMG)

# The icon is rendered by bin/oriel, so an Intel app cross-built on Apple silicon reuses one made by a native build.
$(ICNS): app/icon.go app/titlebar.go
	rm -rf bin/oriel.iconset
	./bin/oriel dev icon bin/oriel.iconset
	iconutil -c icns -o $@ bin/oriel.iconset
else
app dmg:
	@echo "make $@: Oriel.app is built on macOS (codesign, iconutil, hdiutil)" >&2; exit 1
endif

native:
	term/ghostty/native/build.sh $(shell go env GOOS)_$(shell go env GOARCH)

check: fmt-check vet depcheck refcheck vendor-check

fmt-check:
	@out=$$(gofmt -l $$(go list -m -f '{{.Dir}}')); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

depcheck:
	go run ./tools/cmd/depcheck

refcheck:
	go run ./tools/cmd/refcheck

vendor-check:
	go mod verify
	go work vendor
	@git diff --exit-code --stat -- vendor || (echo "vendor/ is out of date: run go work vendor and commit it"; exit 1)
	@test -z "$$(git status --porcelain -- vendor)" || (echo "vendor/ has uncommitted files: run go work vendor and commit it"; exit 1)

vulncheck:
	@set -e; for d in $$(go list -m -f '{{.Dir}}'); do echo "govulncheck $$d"; go run $(GOVULNCHECK) -C $$d ./...; done
