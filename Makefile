BINARY  := git-cli
# git describe gives the nearest tag (+ commits-since + short SHA), falling back
# to a bare SHA before the first tag, and appends -dirty for a modified tree.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install test version

# build stamps the release version; a plain `go build ./cmd/git-cli` still
# reports the commit it came from, just with version "dev".
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/git-cli

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/git-cli

test:
	go test ./...

# Print the version this checkout would stamp.
version:
	@echo $(VERSION)
