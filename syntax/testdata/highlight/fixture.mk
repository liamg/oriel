# Build and test.
GO ?= go
PKGS := ./...

.PHONY: test build

test: build
	$(GO) test $(PKGS)

build:
	$(GO) build -o bin/oriel ./cmd/oriel
