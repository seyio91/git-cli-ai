package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

// Phase 4 criteria. Written before the implementation and expected to fail
// until the PR layer lands.
//
// Every test drives `gh` through a fake installed on a test-controlled PATH,
// so the suite exercises the real argv the tool builds without reaching
// GitHub. newRepo installs a poison pill, so a test that forgets to install a
// fake fails loudly rather than calling out.

const prURL = "https://github.com/owner/repo/pull/7"

// ghCreateSucceeds answers the calls a normal `pr` run makes: no existing PR,
// a known default branch, and a URL from create.
const ghCreateSucceeds = `case "$1 $2" in
  "pr list") echo "[]" ;;
  "repo view") echo '{"defaultBranchRef":{"name":"main"}}' ;;
  "pr create") echo "` + prURL + `" ;;
  *) echo "unexpected gh invocation: $@" >&2; exit 64 ;;
esac`

// onFeatureBranch puts the repo on a pushed feature branch with one commit
// ahead, which is the ordinary state `pr` is run from.
func onFeatureBranch(t *testing.T, repo string) {
	t.Helper()

	mustGit(t, repo, "checkout", "-q", "-b", "feat/thing")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	mustGit(t, repo, "commit", "-qm", "feat(x): add a thing")
}

// withRemote gives the repo an origin that git can push to without a network:
// a bare repository on disk.
func withRemote(t *testing.T, repo string) string {
	t.Helper()

	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", ".")
	mustGit(t, repo, "remote", "add", "origin", remote)
	return remote
}

// SC-24 — pr opens a PR and returns the URL.
func TestSC24_OpensPRAndReturnsURL(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	res := run(t, repo, "pr", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, prURL) {
		t.Fatalf("stdout does not carry the PR URL: %s", res.stdout)
	}

	var created bool
	for _, call := range ghCalls(t, log) {
		if strings.HasPrefix(call, "pr create") {
			created = true
		}
	}
	if !created {
		t.Fatalf("gh pr create was never invoked; calls: %v", ghCalls(t, log))
	}
}

// SC-24 — the tool must never be able to merge. This is the one property the
// whole design is built around, so it is asserted on the argv itself rather
// than trusted to code review.
func TestSC24_NeverInvokesMerge(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	if res := run(t, repo, "pr", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s)", res.exitCode, res.stdout)
	}

	// Match on the subcommand, not the whole argv: a commit subject like
	// "merge upstream into feat" legitimately becomes --title, and asserting
	// over the full line would fail on correct behaviour.
	for _, call := range ghCalls(t, log) {
		fields := strings.Fields(call)
		for i := 0; i < len(fields) && i < 3; i++ {
			if fields[i] == "merge" {
				t.Fatalf("a merge subcommand was invoked: %q", call)
			}
		}
	}
}

// The assertion above must survive a title that merely contains the word.
func TestSC24_MergeCheckToleratesTheWordInATitle(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat(x): merge upstream changes")
	writeFakeGH(t, repo, ghCreateSucceeds)

	if res := run(t, repo, "pr", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
}

// SC-16 — a re-run against an existing open PR returns that URL and does not
// create a second one. Agent retries have to converge.
func TestSC16_ExistingPRIsReturnedNotDuplicated(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	log := writeFakeGH(t, repo, `case "$1 $2" in
  "pr list") echo '[{"url":"`+prURL+`"}]' ;;
  "repo view") echo '{"defaultBranchRef":{"name":"main"}}' ;;
  "pr create") echo "pr create must not run when one already exists" >&2; exit 65 ;;
  *) echo "unexpected gh invocation: $@" >&2; exit 64 ;;
esac`)

	res := run(t, repo, "pr", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 for an existing PR (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, prURL) {
		t.Fatalf("stdout does not carry the existing PR URL: %s", res.stdout)
	}
	if !strings.Contains(res.stdout, `"existing":true`) && !strings.Contains(res.stdout, `"existing": true`) {
		t.Fatalf(`stdout does not report "existing": true: %s`, res.stdout)
	}

	for _, call := range ghCalls(t, log) {
		if strings.HasPrefix(call, "pr create") {
			t.Fatal("a second PR was created for a branch that already had one")
		}
	}
}

// SC-25 — an unpushed branch is pushed with -u before the PR is opened.
func TestSC25_PushesUnpushedBranchBeforeOpening(t *testing.T) {
	repo := newRepo(t)
	remote := withRemote(t, repo)
	onFeatureBranch(t, repo)
	writeFakeGH(t, repo, ghCreateSucceeds)

	// Nothing has been pushed, so the remote has no such branch yet.
	if out := mustGit(t, remote, "branch", "--list", "feat/thing"); strings.TrimSpace(out) != "" {
		t.Fatalf("precondition: remote already has the branch: %q", out)
	}

	res := run(t, repo, "pr", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}

	if out := mustGit(t, remote, "branch", "--list", "feat/thing"); strings.TrimSpace(out) == "" {
		t.Fatal("the branch was never pushed to the remote")
	}
	upstream := strings.TrimSpace(mustGit(t, repo, "rev-parse", "--abbrev-ref", "feat/thing@{upstream}"))
	if upstream != "origin/feat/thing" {
		t.Fatalf("upstream = %q, want origin/feat/thing — push must use -u", upstream)
	}
}

// SC-26 — pr on the default branch errors. Branch creation after the fact is
// ship's job, not pr's.
func TestSC26_DefaultBranchErrors(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	writeFakeGH(t, repo, ghCreateSucceeds)

	branch := strings.TrimSpace(mustGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	writeRepoConfig(t, repo, "[branch]\ndefault_branch = \""+branch+"\"\n")

	res := run(t, repo, "pr", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit when run on the default branch")
	}
	p := res.payload(t)
	if p.Hint == "" {
		t.Fatalf("expected an actionable hint: %s", res.stdout)
	}
}

// SC-28 — an explicit config override decides the default branch without
// asking gh at all.
func TestSC28_ConfigOverrideWinsWithoutCallingGH(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	log := writeFakeGH(t, repo, `case "$1 $2" in
  "pr list") echo "[]" ;;
  "repo view") echo "repo view must not run when default_branch is configured" >&2; exit 66 ;;
  "pr create") echo "`+prURL+`" ;;
  *) echo "unexpected gh invocation: $@" >&2; exit 64 ;;
esac`)
	writeRepoConfig(t, repo, "[branch]\ndefault_branch = \"main\"\n")

	res := run(t, repo, "pr", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	for _, call := range ghCalls(t, log) {
		if strings.HasPrefix(call, "repo view") {
			t.Fatal("gh repo view ran despite an explicit default_branch")
		}
	}
}

// SC-27 — a body that already fills the template is used verbatim and costs no
// provider call.
func TestSC27_TemplateFillingBodyIsUsedVerbatim(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	writeFakeGH(t, repo, ghCreateSucceeds)

	fake, providerLog := writeFakeProvider(t, repo, "fake-provider", "echo 'should never run'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))

	body := "## Summary\nDoes the thing.\n\n## Changes\n- a.txt\n\n## Task\nnone\n\n## Plan\nnone\n\n## Testing\nran the suite\n"
	res := run(t, repo, "pr", "--body", body, "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if calls := providerCalls(t, providerLog); calls != 0 {
		t.Fatalf("provider called %d times for a template-filling body, want 0", calls)
	}
}

// SC-27 — a freeform body is rendered by the generator, grounded on the text
// the author supplied.
func TestSC27_FreeformBodyIsRenderedFromSuppliedIntent(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	writeFakeGH(t, repo, ghCreateSucceeds)

	fake, providerLog := writeFakeProvider(t, repo, "fake-provider", "echo '## Summary\nrendered'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))

	res := run(t, repo, "pr", "--body", "made the widget stop exploding", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if calls := providerCalls(t, providerLog); calls != 1 {
		t.Fatalf("provider called %d times for a freeform body, want exactly 1", calls)
	}
	if prompt := providerPrompts(t, providerLog); !strings.Contains(prompt, "made the widget stop exploding") {
		t.Fatalf("the prompt did not carry the supplied intent: %q", prompt)
	}
}

// SC-29 — --body-file reads the body from disk.
func TestSC29_BodyFileIsRead(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	writeFakeGH(t, repo, ghCreateSucceeds)

	fake, providerLog := writeFakeProvider(t, repo, "fake-provider", "echo 'should never run'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))

	path := filepath.Join(repo, "body.md")
	writeFileAt(t, path, "## Summary\nFrom a file.\n\n## Changes\n- a.txt\n\n## Task\nnone\n\n## Plan\nnone\n\n## Testing\nran it\n")

	res := run(t, repo, "pr", "--body-file", path, "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if calls := providerCalls(t, providerLog); calls != 0 {
		t.Fatalf("provider called %d times, want 0 for a template-filling file", calls)
	}
}

// SC-29 — the two body flags are mutually exclusive, and a missing file is a
// clean error rather than an empty body silently reaching the PR.
func TestSC29_BodyFlagMisuseErrorsCleanly(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"both flags", []string{"pr", "--body", "x", "--body-file", "body.md", "--json"}, "body"},
		{"missing file", []string{"pr", "--body-file", "does-not-exist.md", "--json"}, "does-not-exist.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			withRemote(t, repo)
			onFeatureBranch(t, repo)
			log := writeFakeGH(t, repo, ghCreateSucceeds)

			res := run(t, repo, tc.args...)
			if res.exitCode == 0 {
				t.Fatalf("expected nonzero exit (stdout: %s)", res.stdout)
			}
			if p := res.payload(t); !strings.Contains(p.Error+p.Hint, tc.want) {
				t.Fatalf("error should mention %q: %q / %q", tc.want, p.Error, p.Hint)
			}
			for _, call := range ghCalls(t, log) {
				if strings.HasPrefix(call, "pr create") {
					t.Fatal("a PR was opened despite invalid body flags")
				}
			}
		})
	}
}

// The poison pill has to actually bite, or every assertion above rests on a
// fake that might silently not be installed.
func TestHarnessBlocksRealGH(t *testing.T) {
	repo := newRepo(t)

	out, err := runGH(t, repo)
	if err == nil {
		t.Fatalf("the poison-pill gh exited 0; a test could reach GitHub. output: %s", out)
	}
	if !strings.Contains(out, "must never be called") {
		t.Fatalf("unexpected output from the poison pill: %q", out)
	}
}
