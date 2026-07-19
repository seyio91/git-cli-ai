package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 3 criteria. Written before the implementation exists and expected to
// fail until the AI generation layer lands.
//
// Every test here drives generation through a `cli` provider pointed at a local
// fake, so the suite exercises the full path — prompt assembly, provider call,
// validation, retry — without ever touching the network.

// writeFakeProvider installs an executable stand-in for an AI CLI. The script
// receives the rendered prompt on stdin and prints body on stdout; it records
// each invocation's stdin so a test can assert on what the tool actually sent
// and how many times it called out.
func writeFakeProvider(t *testing.T, repo string, name string, body string) (path string, log string) {
	t.Helper()

	path = filepath.Join(repo, name)
	log = filepath.Join(repo, name+".log")
	script := "#!/bin/sh\ncat >> " + log + "\nprintf '\\n---\\n' >> " + log + "\n" + body + "\n"

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

// providerCalls counts how many times the fake was invoked.
func providerCalls(t *testing.T, log string) int {
	t.Helper()

	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n---\n")
}

func providerPrompts(t *testing.T, log string) string {
	t.Helper()

	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// cliProviderConfig points the active provider at a fake CLI. command is an
// argv array: no shell, so nothing here depends on quoting.
func cliProviderConfig(path string) string {
	return "[ai]\nprovider = \"fake\"\n\n[ai.providers.fake]\ntype = \"cli\"\ncommand = [\"" + path + "\"]\n"
}

// SC-20 — with no --message, the generator supplies the message and the tool
// commits it.
func TestSC20_GeneratesMessageWhenNoneSupplied(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): add the thing'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "feat(api): add the thing") {
		t.Fatalf("stored message = %q, want the generated message", got)
	}
	if calls := providerCalls(t, log); calls != 1 {
		t.Fatalf("provider called %d times, want exactly 1", calls)
	}
}

// SC-20 — the generated message is validated before it can reach a commit. A
// generator is not trusted to produce conforming output.
func TestSC20_GeneratedMessageIsValidatedNotTrusted(t *testing.T) {
	repo := newRepo(t)
	fake, _ := writeFakeProvider(t, repo, "fake-provider", "echo 'just some words'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	before := commitCount(t, repo)
	res := run(t, repo, "commit", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit when the generator returns a non-conforming message")
	}
	if after := commitCount(t, repo); after != before {
		t.Fatalf("an unvalidated generated message reached a commit: %s -> %s", before, after)
	}
}

// SC-20 — the prompt is grounded in the actual diff.
func TestSC20_PromptIncludesTheDiff(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): add the thing'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "distinctive-filename.txt", "a-distinctive-line-of-content\n")
	mustGit(t, repo, "add", "distinctive-filename.txt")

	if res := run(t, repo, "commit", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", res.exitCode, res.stdout)
	}

	prompt := providerPrompts(t, log)
	if !strings.Contains(prompt, "distinctive-filename.txt") {
		t.Fatalf("prompt did not include the changed path: %q", prompt)
	}
	if !strings.Contains(prompt, "a-distinctive-line-of-content") {
		t.Fatalf("prompt did not include the diff content: %q", prompt)
	}
}

// SC-21 — malformed output is re-prompted exactly once, and the retry carries
// the validation failure so the generator can correct itself.
func TestSC21_MalformedOutputIsRetriedExactlyOnce(t *testing.T) {
	repo := newRepo(t)

	// Fails once, then succeeds: the retry must be what lands the commit.
	body := `if [ -f ` + filepath.Join(repo, "attempted") + ` ]; then
  echo 'feat(api): corrected on retry'
else
  touch ` + filepath.Join(repo, "attempted") + `
  echo 'not a conforming message'
fi`
	fake, log := writeFakeProvider(t, repo, "fake-provider", body)
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 after a successful retry (stdout: %s)", res.exitCode, res.stdout)
	}
	if calls := providerCalls(t, log); calls != 2 {
		t.Fatalf("provider called %d times, want exactly 2 (one attempt, one retry)", calls)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "feat(api): corrected on retry") {
		t.Fatalf("stored message = %q, want the corrected message", got)
	}
	if prompt := providerPrompts(t, log); !strings.Contains(prompt, "not a conforming message") {
		t.Fatalf("retry prompt did not carry the rejected message back: %q", prompt)
	}
}

// SC-21 — a generator that stays malformed errors after one retry, showing the
// rejected message. It must not loop.
func TestSC21_PersistentlyMalformedOutputErrorsShowingTheMessage(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'still not conforming'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	before := commitCount(t, repo)
	res := run(t, repo, "commit", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit when the generator never conforms")
	}
	if calls := providerCalls(t, log); calls != 2 {
		t.Fatalf("provider called %d times, want exactly 2 — one retry, never a loop", calls)
	}

	p := res.payload(t)
	if !strings.Contains(p.Error+p.Details, "still not conforming") {
		t.Fatalf("error should show the rejected message: %q / %q", p.Error, p.Details)
	}
	if after := commitCount(t, repo); after != before {
		t.Fatalf("commit count changed on a failed generation: %s -> %s", before, after)
	}
}

// SC-22 — a non-conforming --message is rendered by the generator, and the
// supplied intent reaches the prompt. This is the "no double-AI" rule: the tool
// owns format, the caller owns intent.
func TestSC22_NonConformingMessageIsRenderedFromSuppliedIntent(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): rendered from intent'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", "made the widget stop exploding", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", res.exitCode, res.stdout)
	}
	if prompt := providerPrompts(t, log); !strings.Contains(prompt, "made the widget stop exploding") {
		t.Fatalf("prompt did not carry the supplied intent: %q", prompt)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "feat(api): rendered from intent") {
		t.Fatalf("stored message = %q, want the rendered message", got)
	}
}

// SC-07 regression under Phase 3 — a conforming --message must still bypass the
// provider entirely. The offline path is the agent hot path.
func TestSC22_ConformingMessageStillMakesNoProviderCall(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): should never run'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", "feat(x): supplied and conforming", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", res.exitCode, res.stdout)
	}
	if calls := providerCalls(t, log); calls != 0 {
		t.Fatalf("provider called %d times, want 0 for a conforming message", calls)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "feat(x): supplied and conforming") {
		t.Fatalf("stored message = %q, want the supplied message verbatim", got)
	}
}

// SC-15 — --context-only emits the neutral GenRequest and makes no call and no
// commit.
func TestSC15_ContextOnlyEmitsRequestWithoutCallingOrCommitting(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): should never run'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	before := commitCount(t, repo)
	res := run(t, repo, "commit", "--context-only", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if calls := providerCalls(t, log); calls != 0 {
		t.Fatalf("provider called %d times, want 0 under --context-only", calls)
	}
	if after := commitCount(t, repo); after != before {
		t.Fatalf("--context-only created a commit: %s -> %s", before, after)
	}

	var payload struct {
		Style string `json:"style"`
		Diff  string `json:"diff"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout is not a GenRequest payload (%v): %q", err, res.stdout)
	}
	if payload.Diff == "" {
		t.Fatalf("GenRequest carried no diff: %q", res.stdout)
	}
	if payload.Style == "" {
		t.Fatalf("GenRequest carried no style rules: %q", res.stdout)
	}
}

// SC-15 — --context-only is an inspection mode: it never commits, whatever the
// message situation. A conforming --message short-circuits the generator, which
// must not also short-circuit the flag.
func TestSC15_ContextOnlyNeverCommitsEvenWithAConformingMessage(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): should never run'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	before := commitCount(t, repo)
	res := run(t, repo, "commit", "--context-only", "--message", "feat(x): a conforming message", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if after := commitCount(t, repo); after != before {
		t.Fatalf("--context-only created a commit: %s -> %s", before, after)
	}
	if calls := providerCalls(t, log); calls != 0 {
		t.Fatalf("provider called %d times under --context-only, want 0", calls)
	}

	var payload struct {
		Diff   string `json:"diff"`
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &payload); err != nil {
		t.Fatalf("stdout is not a GenRequest payload (%v): %q", err, res.stdout)
	}
	if payload.Diff == "" {
		t.Fatalf("GenRequest carried no diff: %q", res.stdout)
	}
	if payload.Intent != "feat(x): a conforming message" {
		t.Fatalf("GenRequest intent = %q, want the supplied message", payload.Intent)
	}
}

// SC-15 — an inspection mode must not mutate the index, so --context-only does
// not stage even when --all is passed.
func TestSC15_ContextOnlyWithAllDoesNotStage(t *testing.T) {
	repo := newRepo(t)
	fake, _ := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): should never run'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	writeFile(t, repo, "seed.txt", "modified after the seed commit\n")

	res := run(t, repo, "commit", "--context-only", "--all", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}

	staged := strings.TrimSpace(mustGit(t, repo, "diff", "--cached", "--name-only"))
	if strings.Contains(staged, "seed.txt") {
		t.Fatalf("--context-only --all staged a tracked modification: %q", staged)
	}
}

// SC-14 — the provider is selected by config alone; switching profiles switches
// which one runs, with no flag and no rebuild.
func TestSC14_ProviderSwappableByConfigAlone(t *testing.T) {
	repo := newRepo(t)
	first, firstLog := writeFakeProvider(t, repo, "provider-one", "echo 'feat(api): from provider one'")
	second, secondLog := writeFakeProvider(t, repo, "provider-two", "echo 'feat(api): from provider two'")

	config := "[ai]\nprovider = \"one\"\n\n[ai.providers.one]\ntype = \"cli\"\ncommand = [\"" + first + "\"]\n\n[ai.providers.two]\ntype = \"cli\"\ncommand = [\"" + second + "\"]\n"
	writeRepoConfig(t, repo, config)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	if res := run(t, repo, "commit", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", res.exitCode, res.stdout)
	}
	if providerCalls(t, firstLog) != 1 || providerCalls(t, secondLog) != 0 {
		t.Fatalf("wrong provider ran: one=%d two=%d", providerCalls(t, firstLog), providerCalls(t, secondLog))
	}

	writeRepoConfig(t, repo, strings.Replace(config, "provider = \"one\"", "provider = \"two\"", 1))
	writeFile(t, repo, "b.txt", "b\n")
	mustGit(t, repo, "add", "b.txt")

	if res := run(t, repo, "commit", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", res.exitCode, res.stdout)
	}
	if providerCalls(t, secondLog) != 1 {
		t.Fatalf("switching provider in config did not switch which provider ran: two=%d", providerCalls(t, secondLog))
	}
}

// SC-14 — a profile naming an unset api_key_env fails with a structured error
// that names the variable, rather than panicking or calling out unauthenticated.
func TestSC14_MissingAPIKeyEnvErrorsCleanly(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[ai]\nprovider = \"remote\"\n\n[ai.providers.remote]\ntype = \"openai-compat\"\nbase_url = \"http://127.0.0.1:1\"\napi_key_env = \"GIT_CLI_TEST_UNSET_KEY\"\nmodel = \"test-model\"\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit when the configured api_key_env is unset")
	}

	p := res.payload(t)
	if !strings.Contains(p.Error+p.Hint, "GIT_CLI_TEST_UNSET_KEY") {
		t.Fatalf("error should name the missing env var: %q / %q", p.Error, p.Hint)
	}
	if strings.Contains(strings.ToLower(p.Error+p.Details), "panic") {
		t.Fatalf("missing key produced a panic: %q / %q", p.Error, p.Details)
	}
}

// SC-14 — a provider name with no matching profile is a clear config error.
func TestSC14_UnknownProviderNameErrorsClearly(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[ai]\nprovider = \"does-not-exist\"\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for a provider with no profile")
	}
	if p := res.payload(t); !strings.Contains(p.Error+p.Hint, "does-not-exist") {
		t.Fatalf("error should name the unresolved provider: %q / %q", p.Error, p.Hint)
	}
}

// SC-23 — key material is never accepted from a config file. A profile that
// tries to inline a key must be rejected, not silently honoured, or the
// env-only guarantee is decorative.
func TestSC23_InlineAPIKeyInConfigIsRejected(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[ai]\nprovider = \"remote\"\n\n[ai.providers.remote]\ntype = \"openai-compat\"\nbase_url = \"http://127.0.0.1:1\"\napi_key = \"sk-inline-secret\"\nmodel = \"test-model\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for an inline api_key in config")
	}
	if strings.Contains(res.stdout+res.stderr, "sk-inline-secret") {
		t.Fatalf("the rejected key value was echoed back: %q %q", res.stdout, res.stderr)
	}
}

// SC-23 — the key reaches the provider through the environment only. The cli
// provider inherits auth from the external tool and needs no key at all.
func TestSC23_CLIProviderNeedsNoKey(t *testing.T) {
	repo := newRepo(t)
	fake, _ := writeFakeProvider(t, repo, "fake-provider", "echo 'feat(api): no key needed'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	if res := run(t, repo, "commit", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 for a cli provider with no key configured (stdout: %s)", res.exitCode, res.stdout)
	}
}
