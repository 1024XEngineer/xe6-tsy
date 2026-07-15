GO ?= go
GOFMT ?= gofmt

.PHONY: build fmt test test-race race vet fmt-check verify check run

build:
	mkdir -p bin
	$(GO) build -o bin/xe6-tsy-api ./apps/api/cmd/server

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

test-race: race

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(find apps -name '*.go' -print0 | xargs -0 $(GOFMT) -l)" || (echo "Go files are not formatted" && exit 1)

check: fmt-check vet test race build

verify: fmt vet test test-race build

run:
	$(GO) run ./apps/api/cmd/server
