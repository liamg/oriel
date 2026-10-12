MODULES := $(shell go list -m -f '{{.Path}}/...')

.PHONY: test vet build
test:
	go test $(MODULES)

vet:
	go vet $(MODULES)

build:
	go build -o bin/oriel ./cmd/oriel
