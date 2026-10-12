MODULES := $(shell go list -m -f '{{.Path}}/...')
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: test vet build check fmt-check depcheck refcheck vendor-check vulncheck
test:
	go test $(MODULES)

vet:
	go vet $(MODULES)

build:
	go build -o bin/oriel ./cmd/oriel

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
