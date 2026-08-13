# git-cli

A CLI wrapper around `git` that makes committing and opening pull requests a single, style-consistent command. Its primary consumer is AI agents, but it works standalone.

The tool always owns the *format* of a message; only the *intent* varies. A supplied message that already conforms is used verbatim — no model call, so the offline path stays fast. A freeform message is rendered into the house format. With no message at all, intent is derived from the diff plus the active task context.

It creates and opens pull requests. **It never merges them.**

## Requirements

- Go 1.21+
- `git` on `PATH` (the tool shells out to the real binary rather than reimplementing it)
- `gh` on `PATH`, authenticated, for `pr` and `ship` (GitHub only for now)
- An AI provider is optional: with none configured, every command still works from an explicit `--message` / `--body`.

## Build

```sh
go build -o git-cli ./cmd/git-cli   # reports version "dev" + the commit it came from
make build                          # stamps the release version from git tags
```

## Version

`git-cli version` (or `--version`) reports the version and the commit the binary was built from:

```sh
$ git-cli version
v1.2.0 (a461b0dfd6e4)
$ git-cli version --json
{"version":"v1.2.0","commit":"a461b0dfd6e4b79d9d90a68b03c122e616b52c8a","dirty":false}
```

The commit SHA and a `dirty` flag are embedded automatically from the build's VCS info, so even a plain `go build` tells you exactly which commit — and whether the tree was modified — a binary came from. The `version` field is `dev` for an ordinary build and is stamped from git tags on a release build (`make build`, via `-ldflags "-X main.version=$(git describe --tags --always --dirty)"`).

**Cutting a release:** tag the commit and build.

```sh
git tag v1.2.0
make build          # or: make install
```

## Commands

`commit`, `pr`, `ship`, `config`, and `version`. `--json` and `--dry-run` are global and may appear on either side of the subcommand.

### `commit`

Create a commit whose message conforms to the configured style.

```sh
git-cli commit -m "feat(api): add pagination"
```

- A conforming message is used verbatim — no model call.
- A freeform message is rendered into the configured style, grounded on your words, when a provider is configured.
- With no `-m` and a provider configured, the message is generated from the staged diff plus task context.

| Flag | Effect |
|------|--------|
| `-m`, `--message` | The commit message, or the freeform intent to render. |
| `--all` | Stage tracked modified and deleted files first. Untracked files are **never** staged implicitly. |
| `--context-only` | Emit the provider-neutral generation request (style rules + diff + context) as JSON and stop. No provider call, no commit — for a calling agent to write the message itself. |

Committing with nothing staged is an error that lists the unstaged changes.

### `pr`

Open a pull request for the current branch via `gh`, and return its URL.

```sh
git-cli pr --title "Add pagination" --body-file PR.md
```

- The body comes from `--body` / `--body-file` rendered into the configured template, or is generated from the diff and context when absent.
- An unpushed branch is pushed with `-u` first.
- If an open PR already exists for the branch, its URL is returned (`"existing": true` in `--json`) — re-runs converge instead of duplicating.
- Running on the default branch is an error: `pr` does not create branches (use `ship`).

| Flag | Effect |
|------|--------|
| `--title` | PR title. Defaults to the latest commit subject. |
| `--body` | PR body, or the intent to render into the template. |
| `--body-file` | Read the body from a file. Mutually exclusive with `--body`. |
| `--base` | Base branch. Defaults to the repository's detected default branch. |

### `ship`

Stage, commit, push, and open a PR in one step — then stop. **`ship` never merges.**

```sh
git-cli ship -m "feat(api): add pagination"
```

- On a feature branch: commits the staged changes and opens the PR.
- On the default branch: derives a branch name from the resolved message (see `branch.pattern`), creates it, and commits there — **never onto the default branch**.
- Converges on retry: an already-open PR is returned, a pushed branch is reused, and a partial run (commits made but not pushed) resumes at the push.

Takes the union of `commit`'s and `pr`'s flags: `-m`/`--message`, `--all`, `--title`, `--body`/`--body-file`, `--base`.

`--dry-run` previews the resolved message, the derived branch name, the push target, and the PR title, and mutates nothing.

### `config`

Print the resolved configuration with the layer each value came from. Exits nonzero if any layer is invalid, so it doubles as a validator.

```sh
git-cli config --json
```

## Configuration

TOML, resolved with precedence **repo `.git-cli.toml` > global `~/.config/git-cli/config.toml` > built-in defaults**, merged per key. Absent files and absent keys fall back to defaults; overriding one key leaves its siblings intact, so a repo file need only state what it changes.

Decoding is strict — an unknown key is an error naming both the key and the file, so a typo fails loudly instead of being silently ignored.

Ready-to-copy examples are in [`examples/`](examples/): [`config.global.toml`](examples/config.global.toml) and [`config.repo.toml`](examples/config.repo.toml).

| Key | Default | Meaning |
|-----|---------|---------|
| `commit.style` | `conventional-commits` | `conventional-commits`, `gitmoji`, or `freeform-with-rules`. |
| `branch.pattern` | `{type}/{slug}` | Name `ship` gives a branch it creates. Must contain `{type}` and/or `{slug}`. |
| `branch.default_branch` | *(detect)* | Override the default branch. Empty detects it via `origin/HEAD`, then `gh`. |
| `pr.template` | *(built-in)* | Path to a PR body template. Empty uses the built-in (Summary / Changes / Task / Plan / Testing). |
| `ai.provider` | `openai` | Active provider profile by name. |
| `ai.model` | — | Model for both tasks, unless a per-task model is set. |
| `ai.commit_model` | `gpt-4.1-mini` | Model for commit messages. |
| `ai.pr_model` | `gpt-4.1` | Model for PR bodies. |
| `ai.providers.<name>` | `openai`, `anthropic` built in | Named provider profiles. |

### Providers

A provider profile has a `type`:

- **`openai-compat`** — one HTTP path for OpenAI, Gemini's compatibility endpoint, Groq, OpenRouter, and local Ollama / LM Studio. Needs `base_url` and `api_key_env`. Some newer models reject `max_tokens` and require `max_completion_tokens`; set `max_tokens_param` on the profile when so.
- **`anthropic`** — the first-party Anthropic API. Needs `api_key_env`. Written and unit-tested against an in-process server, but **not yet verified against a live endpoint** — the `openai-compat` and `cli` paths have been exercised end to end, this one has not.
- **`cli`** — shell out to an existing tool (reusing its own auth). Needs `command` as an argv array, e.g. `["claude", "-p"]`.

**API keys are never stored in config.** A profile names the environment variable to read its key from (`api_key_env`); the key itself lives only in the environment. The `openai` and `anthropic` profiles are built in and resolve with no `[ai.providers]` block, keyed off `OPENAI_API_KEY` and `ANTHROPIC_API_KEY`.

## Memory / task awareness

When a repository is pinned to a project in the AI memory system, `commit` and `pr` inject the active task, the plan's goal, and the project's goal into the generation prompt, and the PR body auto-links task and plan.

Pinning is a file `.agents/memory-project` at the repo root containing the bare project name. The project is resolved under `$AI_MEMORY_ROOT`, then `$MEMORY_DIR`, then `~/.claude-memory` (`<base>/projects/<name>/`). A repo with no marker — the ordinary case — behaves exactly the same, on the diff alone, with no error.

## Agent notes

- `--json` on every command, non-interactive by default.
- Failures emit `{"error", "hint", "details"}` with a nonzero exit. `hint` is actionable guidance; `details` carries the underlying tool's own output.
- `--dry-run` previews and mutates nothing.
- `commit --context-only` hands the generation request to a calling agent instead of calling a provider.
- Operations converge on retry rather than fail on a second run. No command ever merges a pull request.

## Testing

```sh
go test ./...
```

`acceptance/` drives the compiled binary against throwaway repositories. Each test is named for the success criterion it covers, so a criterion without a test is visible as a gap.
