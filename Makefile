GO ?= go
GOFMT ?= gofmt

.PHONY: build check fmt fmt-check run test test-race vet

build:
	mkdir -p bin
	$(GO) build -o bin/xe6-tsy-api ./apps/api/cmd/server

fmt:
	$(GOFMT) -w $$(find apps -name '*.go' -type f)

fmt-check:
	@test -z "$$(find apps -name '*.go' -type f -print0 | xargs -0 $(GOFMT) -l)" || (echo "Go files are not formatted" && exit 1)

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

check: fmt-check vet test test-race build

run:
	$(GO) run ./apps/api/cmd/server
