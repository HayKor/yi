VERSION ?= dev

.PHONY: build test lint release-snapshot

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o yi ./cmd/yi

test:
	go test ./...

lint:
	go vet ./...

release-snapshot:
	goreleaser release --snapshot --clean
