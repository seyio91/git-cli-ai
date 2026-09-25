# git-cli

One command to commit and open a pull request in a consistent style. Built for AI agents, usable by hand. It opens pull requests and **never merges them**.

git-cli owns the format of a commit message and takes the intent from you:

- a message that already conforms is used as is, with no model call
- a freeform message is rewritten into the house style
- no message at all is generated from the diff

A pull request body passed with `--body` or `--body-file` is always posted verbatim. Pass `--intent` to have one written.

## Install

Requires Go 1.21+, `git`, and an authenticated `gh` for `pr` and `ship`.

```sh
make build        # or: go build -o git-cli ./cmd/git-cli
```

An AI provider is optional. Without one, every command works from an explicit `-m` or `--body`.

## Usage

```sh
git-cli commit -m "feat(api): add pagination"
git-cli pr --intent "adds cursor pagination to the list endpoints"
git-cli ship -m "feat(api): add pagination"      # stage, commit, branch, push, open PR
git-cli pr --update --body-file PR.md            # rewrite an open PR's body
git-cli config                                   # show resolved config, validate it
```

`--json` and `--dry-run` work on every command.

## Configuration

TOML. The repo's `.git-cli.toml` overrides `~/.config/git-cli/config.toml`, which overrides the built-in defaults, key by key. See [`examples/`](examples/) for files you can copy.

## Docs

- [Commands](docs/commands.md): every flag, and how `ship` resumes after a failure
- [Configuration](docs/configuration.md): keys, AI providers, memory context
- [For agents](docs/agents.md): JSON output and the error contract
- [Development](docs/development.md): building, versioning, tests
