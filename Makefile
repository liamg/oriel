GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: test test-shard test-chaos perf test-framecheck test-vt test-voice vet helper build run app dmg native oracle check fmt-check depcheck refcheck leakcheck vendor-check notices notices-check vulncheck lint

GOARCH ?= $(shell go env GOARCH)
VERSION ?= dev
HELPER := sandbox/helper/bin/oriel-sandbox-linux-$(GOARCH)
# RELEASE_KEYS (hex ed25519 public keys, comma-separated) gives the build its updater; SANDBOX_DIGEST pins the
# published sandbox base image it pulls (sha256:…). release.yml sets both.
LDFLAGS := -X github.com/liamg/oriel/cmd/oriel/cli.version=$(VERSION) $(if $(RELEASE_KEYS),-X github.com/liamg/oriel/update.releaseKeys=$(RELEASE_KEYS)) \
	$(if $(SANDBOX_DIGEST),-X github.com/liamg/oriel/sandbox/container.baseDigest=$(SANDBOX_DIGEST))
# test runs the suite in a random order (its seed is printed), so a test that leans on another's leftovers fails.
# Nothing in it measures the machine: those are make perf's.
test:
	go test -shuffle=on ./...

# test-shard runs half the suite, SHARD=1 or SHARD=2, so CI can run the halves at once. Shard 1 is the slowest app
# packages, listed; shard 2 is everything else, so a new package lands there without a change here.
SHARD1 := ./app/ship ./app/agentui ./app/sessions ./app/shell ./app/lang ./app/explain ./app/review
test-shard:
	@case "$(SHARD)" in \
		1) go test -shuffle=on $(SHARD1) ;; \
		2) one=$$(go list $(SHARD1)) && go test -shuffle=on $$(go list ./... | grep -vxF "$$one") ;; \
		*) echo "make test-shard: SHARD=1 or SHARD=2" >&2; exit 2 ;; \
	esac

# test-chaos runs the suite with the work started off the loop (plugin.Go, plugin.Load, kit.Busy, a plugin.Cache's
# fetches, the host's jobs) slowed by a random while: a test that passes only because that work was quick fails here,
# not on a loaded CI runner.
test-chaos:
	ORIEL_TEST_CHAOS=60 go test -shuffle=on -count=1 ./...

# perf runs the tests that measure the machine rather than the code: time, throughput and allocation budgets. Each
# lives in a _perf_test.go file built only with the perf tag and is named …Budget. Run it on a quiet machine; CI
# does not.
perf:
	go test -tags perf -p 1 -count=1 -run 'Budget$$' ./...

# test-framecheck runs the snapshot suites with fsx's frame check on: I/O through fsx in a frame panics.
test-framecheck:
	go test -p 1 -tags oriel_framecheck -run 'Snapshot|Golden' ./app/... ./internal/fsx/...

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

check: vet lint

# lint is check without vet: what doesn't depend on the platform, so CI runs it once.
lint: fmt-check depcheck refcheck leakcheck vendor-check notices-check

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
