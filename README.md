# git-cli

A CLI wrapper around `git` that makes committing and opening pull requests a single, style-consistent command. Its primary consumer is AI agents, but it works standalone.

The tool always owns the *format* of a message; only the *intent* varies. A supplied message that already conforms is used verbatim — no model call, so the offline path stays fast. A freeform message is rendered into the house format. With no message at all, intent is derived from the diff plus the active task context.

It creates and opens pull requests. **It never merges them.**

## Status

Early. Phases 1 and 2 of the v1 plan are complete and tested; the rest is not built yet.

| Area | State |
|------|-------|
| `commit` with an explicit `--message` | ✅ working |
| Conventional Commits validation | ✅ working |
| Layered TOML configuration | ✅ working |
| `config` subcommand | ✅ working |
| AI message generation | ❌ not started |
| `pr` — open a pull request | ❌ not started |
| `ship` — stage → commit → push → PR | ❌ not started |

Config values currently resolve but are not yet consumed by `commit`.

## Requirements

- Go 1.21+
- `git` on `PATH` (the tool shells out to the real binary rather than reimplementing it)

## Build

```sh
go build -o git-cli ./cmd/git-cli
```

## Usage

### `commit`

```sh
git-cli commit --message "feat(api): add pagination"
```

A message must conform to Conventional Commits — `type(scope): subject`, with an optional body after a blank line:

```sh
git-cli commit --message "$(printf 'feat(api)!: drop v1\n\nBREAKING CHANGE: v1 removed.')"
```

| Flag | Effect |
|------|--------|
| `--message`, `-m` | The commit message (required for now) |
| `--all` | Stage tracked modified and deleted files first. Untracked files are **never** staged implicitly. |
| `--dry-run` | Print what would happen; change nothing |
| `--json` | Emit machine-readable output |

Committing with nothing staged is an error that lists the unstaged changes.

### `config`

Prints the resolved configuration and the layer each value came from. Exits nonzero if any layer is invalid, so it doubles as a validator.

```sh
git-cli config --json
```

## Configuration

TOML, resolved in order — later layers win, **merged per key**:

1. Built-in defaults
2. `$XDG_CONFIG_HOME/git-cli/config.toml` (falls back to `~/.config/git-cli/config.toml`)
3. `.git-cli.toml` at the repository root

Overriding one key leaves its siblings intact, so a repo file need only state what it changes.

```toml
[commit]
style = "conventional-commits"  # or gitmoji, freeform-with-rules

[ai]
provider = "anthropic"
commit_model = "claude-haiku-4-5"
pr_model = "claude-sonnet-5"

[branch]
pattern = "{type}/{slug}"
```

Decoding is strict — an unknown key is an error naming both the key and the file, so a typo fails loudly instead of being silently ignored.

**API keys are read from environment variables only.** Config files reference the variable name; key material never belongs in them.

## Agent notes

- `--json` on every command, non-interactive by default.
- Failures emit `{"error", "hint", "details"}` with a nonzero exit. `hint` is actionable guidance; `details` carries the underlying tool's own output.
- Operations are designed to converge on retry rather than fail on a second run.

## Testing

```sh
go test ./...
```

`acceptance/` drives the compiled binary against throwaway repositories. Each test is named for the success criterion it covers, so a criterion without a test is visible as a gap.
