MODULES := $(shell go list -m -f '{{.Path}}/...')
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: test test-vt vet build native oracle check fmt-check depcheck refcheck vendor-check vulncheck
test:
	go test $(MODULES)

# test-vt runs the terminal emulator's suite on both backends; it needs native first.
test-vt:
	go test -tags ghostty_native ./term/...

oracle:
	ORIEL_ORACLE=1 go test ./vim/...

vet:
	go vet $(MODULES)

# build is the desktop app, on the native libghostty-vt; it needs native first. Without the tag (or cgo) the
# emulator runs as wasm.
build:
	go build -tags ghostty_native -o bin/oriel ./cmd/oriel

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
