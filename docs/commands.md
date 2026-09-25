# Commands

`commit`, `pr`, `ship`, `config` and `version`. `--json` and `--dry-run` are global and may appear before or after the subcommand.

## commit

Create a commit whose message conforms to the configured style.

```sh
git-cli commit -m "feat(api): add pagination"
```

- A conforming message is used verbatim, with no model call.
- A freeform message is rewritten into the configured style, using your words, when a provider is configured.
- With no `-m`, the message is generated from the staged diff and any task context.
- If `commit.types` is set, a message with an unlisted type is refused rather than rewritten.

| Flag | Effect |
|------|--------|
| `-m`, `--message` | The commit message, or freeform intent to rewrite. |
| `--all` | Stage tracked modified and deleted files first. Untracked files are never staged. |
| `--context-only` | Print the generation request (style rules, diff, context) as JSON and stop. No provider call and no commit, so a calling agent can write the message itself. |

Committing with nothing staged is an error that lists the unstaged changes.

## pr

Open a pull request for the current branch with `gh` and print its URL.

```sh
git-cli pr --title "Add pagination" --body-file PR.md
```

- `--body` and `--body-file` are posted byte for byte, with no provider call. `--intent` has a body written from a rough description. With neither, the body is generated from the diff.
- An unpushed branch is pushed with `-u` first.
- If an open PR already exists for the branch, its URL is returned (`"existing": true` in `--json`) and nothing changes.
- Running on the default branch is an error. Use `ship` to create a branch.

| Flag | Effect |
|------|--------|
| `--title` | PR title. Defaults to the latest commit subject. |
| `--body` | PR body, used verbatim. |
| `--body-file` | Read the verbatim body from a file. |
| `--intent` | Describe the change and have the provider write the body. |
| `--base` | Base branch. Defaults to the repository's default branch. |
| `--draft` | Open as a draft. Has no effect on an existing PR. |
| `--update` | Rewrite the body of the already-open PR. |

`--body`, `--body-file` and `--intent` are mutually exclusive.

### Updating an open PR

Supplying a body or intent for a branch that already has an open PR is an error unless you pass `--update`. With it:

- `--intent` regenerates the body, `--body`/`--body-file` replace it verbatim, and bare `--update` regenerates it from the diff.
- The title changes only when `--title` is given.
- `--dry-run --update` reports what would change without contacting the forge or a provider.

## ship

Stage, commit, push and open a PR in one step. `ship` never merges.

```sh
git-cli ship -m "feat(api): add pagination"
```

- On a feature branch it commits and opens the PR.
- On the default branch it creates a branch named from the message (see `branch.pattern`) and commits there. It never commits to the default branch.
- A re-run returns an open PR, reuses a pushed branch, and resumes at the push if commits were made but not pushed.

Takes the flags of both `commit` and `pr`: `-m`, `--all`, `--title`, `--body`, `--body-file`, `--intent`, `--base`, `--draft`, `--update`.

### Resuming after a failure

`ship` runs four steps and only the last is safe to retry. If it fails after the branch, commit or push has happened, the error reports what completed and the command to resume with:

```
Error: ai provider "litellm" returned HTTP 403
Hint: the response was an HTML page rather than an API error, so the request probably never reached ai provider "litellm"; check the network path (VPN, proxy, DNS) before changing any credential
Completed: created branch feat/add-pagination; committed on feat/add-pagination; pushed feat/add-pagination to origin
Resume: git-cli pr
```

Running `ship` again after a push would commit a second time. Run the `Resume` command instead.

`--dry-run` shows the resolved message, branch name, push target and PR title, and changes nothing.

## config

Print the resolved configuration and the layer each value came from. Exits nonzero if any layer is invalid, so it also works as a validator.

```sh
git-cli config --json
```

## version

Print the version and the commit the binary was built from. See [Development](development.md#versioning).
