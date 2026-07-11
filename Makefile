GOCACHE ?= /tmp/trustpanel-go-cache

.PHONY: test build build-trusttunnel release-bins run-panel run-fallback run-bot

test:
	GOCACHE=$(GOCACHE) go test ./...

build:
	GOCACHE=$(GOCACHE) go build -buildvcs=false -o bin/trustpanel ./cmd/trustpanel

# Cross-compile the release binaries the GitHub release ships (see release.yml).
release-bins:
	GOCACHE=$(GOCACHE) GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w" -o bin/trustpanel-linux-amd64 ./cmd/trustpanel
	GOCACHE=$(GOCACHE) GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w" -o bin/trustpanel-linux-arm64 ./cmd/trustpanel

build-trusttunnel:
	bash scripts/build-trusttunnel.sh

run-panel:
	GOCACHE=$(GOCACHE) go run ./cmd/trustpanel serve --listen 127.0.0.1:8787 --data-dir /tmp/trustpanel-dev

run-fallback:
	GOCACHE=$(GOCACHE) go run ./cmd/trustpanel fallback --listen 127.0.0.1:8080

run-bot:
	GOCACHE=$(GOCACHE) go run ./cmd/trustpanel bot --listen 127.0.0.1:8791
