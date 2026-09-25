# Configuration

Configuration is TOML, read from three layers. The repo's `.git-cli.toml` wins over the global `~/.config/git-cli/config.toml` (or `$XDG_CONFIG_HOME/git-cli/config.toml`), which wins over the built-in defaults. Layers merge key by key, so a repo file only needs the keys it changes.

Decoding is strict: an unknown key is an error that names the key and the file. A binary older than a key will therefore reject a config that uses it, so rebuild before adding new keys.

Copyable examples: [`config.global.toml`](../examples/config.global.toml), [`config.repo.toml`](../examples/config.repo.toml).

## Keys

| Key | Default | Meaning |
|-----|---------|---------|
| `commit.style` | `conventional-commits` | `conventional-commits`, `gitmoji` or `freeform-with-rules`. |
| `commit.types` | any | Allowed conventional types, e.g. `["feat", "fix", "chore"]`. An empty list is an error. |
| `branch.pattern` | `{type}/{slug}` | Name `ship` gives a branch it creates. Must contain `{type}` or `{slug}`. |
| `branch.default_branch` | detected | Override the default branch. Otherwise read from `origin/HEAD`, then `gh`. |
| `pr.template` | built in | Path to a PR body template. The built-in has Description, Changes, Prerequisites and Ordering. |
| `ai.provider` | `openai` | Active provider profile. |
| `ai.model` | | Model for both commits and PRs, unless a per-task model is set. |
| `ai.commit_model` | `gpt-4.1-mini` | Model for commit messages. |
| `ai.pr_model` | `gpt-4.1` | Model for PR bodies. |
| `ai.providers.<name>` | `openai`, `anthropic` | Named provider profiles. |

### commit.types

Unset, the validator checks that a type is well formed but not what it is called, so a generated message can invent one. Set it and the list is enforced and included in the prompt. A supplied `-m` with an unlisted type is refused rather than rewritten, since it already states a type. Because `branch.pattern` defaults to `{type}/{slug}`, the list also limits the branch prefixes `ship` can create.

## Providers

Each profile has a `type`:

- `openai-compat`: OpenAI, Gemini's compatibility endpoint, Groq, OpenRouter, Ollama and LM Studio. Needs `base_url` and `api_key_env`. Some newer models reject `max_tokens`; set `max_tokens_param = "max_completion_tokens"` for those.
- `anthropic`: the Anthropic Messages API. Needs `api_key_env`; `base_url` is optional. Tested through an Anthropic-compatible proxy, not yet against `api.anthropic.com` directly.
- `cli`: runs an existing tool with the prompt on stdin, reusing its auth. Needs `command` as an argv array, e.g. `["claude", "-p"]`.

API keys are never stored in config. A profile names the environment variable holding its key in `api_key_env`, and an inline `api_key` is rejected. The built-in `openai` and `anthropic` profiles read `OPENAI_API_KEY` and `ANTHROPIC_API_KEY`.

A declared profile uses its own `model` for both commits and PRs; `ai.commit_model` and `ai.pr_model` apply only to the built-in profiles.

## Memory context

A repository can be pinned to a project in the AI memory system with a file `.agents/memory-project` containing the project name. The project is looked up under `$AI_MEMORY_ROOT`, then `$MEMORY_DIR`, then `~/.claude-memory`, at `<base>/projects/<name>/`.

When pinned, `commit` and `pr` add the active task, the plan's goal and the project's goal to the prompt. This is input only. The built-in template has no task or plan section, because the active task is often unrelated to the diff. A custom `pr.template` can include `{{task}}` and `{{plan}}`.

Without a marker, or with an invalid one, generation uses the diff alone and reports no error.
