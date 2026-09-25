# For agents

- Every command accepts `--json` and never prompts.
- A failure prints `{"error", "hint", "details"}` and exits nonzero. `hint` says what to do; `details` holds the underlying tool's output. Git failures add `git_exit_code`.
- When a composite command fails after doing durable work, the error adds `completed` and `resume`. Read them before retrying: re-running `ship` after a push commits twice, while `resume` carries on from where it stopped.
- A non-2xx provider response whose body is HTML is reported as a network problem rather than a bad key. A corporate gateway with the VPN down answers `403` this way.
- `--dry-run` previews and changes nothing.
- `commit --context-only` prints the generation request so the agent can write the message itself.
- Commands converge on retry: a second `pr` returns the existing PR instead of opening another.
- Nothing merges a pull request, and the provider interface has no merge method.
