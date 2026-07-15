.PHONY: fmt test vet build check

fmt:
	go fmt ./...

test:
	go test ./...

vet:
	go vet ./...

build:
	go build ./apps/api/cmd/server

check: test vet build
