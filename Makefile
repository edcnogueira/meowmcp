VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
TAG ?= main

.PHONY: build test run-http image deploy

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/meowmcp ./cmd/meowmcp

test:
	go test ./...

run-http: build
	./bin/meowmcp -transport http

image:
	docker build --build-arg VERSION=$(VERSION) -t meowmcp:$(VERSION) .

# Pulls the CI-built image on the homelab. TAG=sha-1a2b3c4 or TAG=0.2.0 pins/rolls back.
deploy:
	deploy/deploy.sh $(TAG)
