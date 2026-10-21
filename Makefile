VERSION ?= dev

.PHONY: build test lint

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o yi ./cmd/yi

test:
	go test ./...

lint:
	go vet ./...
