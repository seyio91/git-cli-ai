package main

import "github.com/seyio91/git-cli-ai/internal/cli"

// version is "dev" for an ordinary build and is stamped at release build time
// with -ldflags "-X main.version=$(git describe --tags --always --dirty)". The
// commit SHA and dirty flag are read from the embedded build info either way,
// so even a plain `go build` reports which commit it came from.
var version = "dev"

func main() {
	cli.Execute(version)
}
