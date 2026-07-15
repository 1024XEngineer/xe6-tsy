GO ?= go
GOFMT ?= gofmt

.PHONY: build test race vet fmt-check check run

build:
	$(GO) build ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(find apps -name '*.go' -print0 | xargs -0 $(GOFMT) -l)" || (echo "Go files are not formatted" && exit 1)

check: fmt-check vet test race build

run:
	$(GO) run ./apps/api/cmd/server
