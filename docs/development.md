# Development

## Building

```sh
go build -o git-cli ./cmd/git-cli   # version "dev"
make build                          # version stamped from git tags
make install                        # go install with the same stamp
```

## Versioning

```sh
$ git-cli version
v1.2.0 (a461b0dfd6e4)
$ git-cli version --json
{"version":"v1.2.0","commit":"a461b0dfd6e4b79d9d90a68b03c122e616b52c8a","dirty":false}
```

The commit and `dirty` flag come from the build's VCS info, so any build shows which commit it was made from. `version` is `dev` for a plain build and is set by `make build` through `-ldflags "-X main.version=$(git describe --tags --always --dirty)"`.

To cut a release, tag the commit and run `make build`.

## Tests

```sh
go test -count=1 ./...
```

`acceptance/` builds the binary and runs it against throwaway repositories. Each test is named for the success criterion it covers.

Use `-count=1`. The acceptance suite compiles the binary itself, so Go's test cache does not see changes under `internal/` and can report a stale pass.
