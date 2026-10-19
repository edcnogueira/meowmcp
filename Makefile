VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test run-http

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/meowmcp ./cmd/meowmcp

test:
	go test ./...

run-http: build
	./bin/meowmcp -transport http
