GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: test perf test-framecheck test-vt test-voice vet helper build run app dmg native oracle check fmt-check depcheck refcheck leakcheck vendor-check notices notices-check vulncheck

GOARCH ?= $(shell go env GOARCH)
VERSION ?= dev
HELPER := sandbox/helper/bin/oriel-sandbox-linux-$(GOARCH)
# RELEASE_KEYS (hex ed25519 public keys, comma-separated) gives the build its updater; SANDBOX_DIGEST pins the
# published sandbox base image it pulls (sha256:…). release.yml sets both.
LDFLAGS := -X github.com/liamg/oriel/cmd/oriel/cli.version=$(VERSION) $(if $(RELEASE_KEYS),-X github.com/liamg/oriel/update.releaseKeys=$(RELEASE_KEYS)) \
	$(if $(SANDBOX_DIGEST),-X github.com/liamg/oriel/sandbox/container.baseDigest=$(SANDBOX_DIGEST))
# test runs the timing budgets (tests named …Budget, perf.Timed) after the rest, one package at a time, so other
# packages' tests running alongside can't fail them. Tests run in a random order (its seed is printed), so one that
# leans on another's leftovers fails.
test:
	ORIEL_BUDGETS=later go test -shuffle=on ./...
	go test -p 1 -shuffle=on -run 'Budget$$' ./...

# perf runs every timing budget with the wall-clock ones (build tag perf): latency and idle checks only a real clock
# can measure, kept out of make test so it stays deterministic. Run it on a quiet machine; CI does not.
perf:
	go test -tags perf -p 1 -count=1 -run 'Budget$$' ./...

# test-framecheck runs the snapshot and lag suites with fsx's frame check on: I/O through fsx in a frame panics.
test-framecheck:
	go test -p 1 -tags oriel_framecheck -run 'Snapshot|Golden|Lag|Budget' ./app/... ./internal/fsx/...

# test-vt runs the terminal emulator's suite on both backends; it needs native first.
test-vt:
	go test -tags ghostty_native ./term/...

# test-voice runs voice's suite on the built-in whisper.cpp; it needs native first, and a model to transcribe with in
# ORIEL_WHISPER_MODEL (a ggml file, such as ggml-tiny.en.bin) or the native test skips.
test-voice:
	go test -tags whisper_native ./voice/...

oracle:
	ORIEL_ORACLE=1 go test ./vim/...

vet:
	go vet ./...

# helper is the sandbox helper the app embeds (sandbox/helper): cmd/oriel-sandbox, static, for Linux on the app's
# arch, which docker sandboxes run as their oriel.
helper:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -buildvcs=false -ldflags=-s -o $(HELPER) ./sandbox/cmd/oriel-sandbox
	gzip -9nf $(HELPER)

# build is the desktop app, on the native libghostty-vt and whisper.cpp, with its sandbox helper; it needs native
# first. Without the tags (or cgo) the emulator runs as wasm and dictation needs whisper-server. GOARCH=amd64 on an
# Apple-silicon Mac builds the Intel app.
build: helper
	@test -f term/ghostty/native/include/ghostty/vt.h && test -f term/ghostty/native/lib/$(shell go env GOOS)_$(GOARCH)/libghostty-vt.a || \
		{ echo "make build: libghostty-vt isn't built for $(shell go env GOOS)_$(GOARCH): run make native first (it needs zig 0.16.0)" >&2; exit 1; }
	@test -f voice/native/include/whisper.h && test -f voice/native/lib/$(shell go env GOOS)_$(GOARCH)/libwhisper.a || \
		{ echo "make build: whisper.cpp isn't built for $(shell go env GOOS)_$(GOARCH): run make native first (it needs cmake)" >&2; exit 1; }
	go build -tags "ghostty_native whisper_native" -ldflags "$(LDFLAGS)" -o bin/oriel ./cmd/oriel

# run builds the app and runs it: make run ARGS=~/src/project
run: build
	./bin/oriel $(ARGS)

# app is bin/Oriel.app, signed with CODESIGN_IDENTITY (ad hoc by default; release.yml uses the self-signed one), and
# dmg packs it for first install. macOS only.
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
$(ICNS): app/shell/icon.go app/kit/parts.go
	rm -rf bin/oriel.iconset
	./bin/oriel dev icon bin/oriel.iconset
	iconutil -c icns -o $@ bin/oriel.iconset
else
app dmg:
	@echo "make $@: Oriel.app is built on macOS (codesign, iconutil, hdiutil)" >&2; exit 1
endif

native:
	term/ghostty/native/build.sh $(shell go env GOOS)_$(shell go env GOARCH)
	voice/native/build.sh $(shell go env GOOS)_$(shell go env GOARCH)

check: fmt-check vet depcheck refcheck leakcheck vendor-check notices-check

fmt-check:
	@out=$$(gofmt -l $$(go list -f '{{.Dir}}' ./...)); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

depcheck:
	go run ./tools/cmd/depcheck

refcheck:
	go run ./tools/cmd/refcheck

# leakcheck denies private names in tracked files; CI passes the names in ORIEL_LEAK_DENYLIST.
leakcheck:
	go run ./tools/cmd/leakcheck

# notices writes THIRD_PARTY_NOTICES from vendor/ and the embedded parts; notices-check fails if it's stale.
notices:
	go run ./tools/cmd/notices

notices-check:
	go run ./tools/cmd/notices -check

vendor-check:
	go mod verify
	go mod vendor
	@git diff --exit-code --stat -- vendor || (echo "vendor/ is out of date: run go mod vendor and commit it"; exit 1)
	@test -z "$$(git status --porcelain -- vendor)" || (echo "vendor/ has uncommitted files: run go mod vendor and commit it"; exit 1)

vulncheck:
	go run $(GOVULNCHECK) ./...
