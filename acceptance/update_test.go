package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Criteria for `--update`: editing the prose of a pull request that is already
// open. Before this existed the only path was `gh pr edit`, and prose passed to
// a converging run was dropped on an exit 0.

// withExistingPR installs a gh that reports one open pull request on the branch
// and captures whatever `pr edit` is fed on stdin. The body travels through
// `--body-file -`, so the argv log alone says nothing about what was written.
func withExistingPR(t *testing.T, repo string) (log string, editBody string) {
	t.Helper()

	editBody = filepath.Join(ghBinDir(repo), "gh.edit")
	log = writeFakeGH(t, repo, `case "$1 $2" in
  "pr list") echo '[{"url":"`+prURL+`","baseRefName":"main"}]' ;;
  "repo view") echo '{"defaultBranchRef":{"name":"main"}}' ;;
  "pr edit") cat > `+editBody+` ;;
  "pr create") echo "pr create must not run when one already exists" >&2; exit 65 ;;
  *) echo "unexpected gh invocation: $@" >&2; exit 64 ;;
esac`)
	return log, editBody
}

// editedBody is what the tool actually sent to `gh pr edit`.
func editedBody(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("gh pr edit was never fed a body: %v", err)
	}
	return string(data)
}

// editArgv returns the argv of the single `pr edit` call, and whether one
// happened at all.
func editArgv(t *testing.T, log string) (string, bool) {
	t.Helper()

	for _, call := range ghCalls(t, log) {
		if strings.HasPrefix(call, "pr edit") {
			return call, true
		}
	}
	return "", false
}

// U1 — --update --intent replaces the body with generated prose.
func TestUpdate_IntentRewritesTheBody(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	sentinelProvider(t, repo)
	log, editBody := withExistingPR(t, repo)

	res := run(t, repo, "pr", "--update", "--intent", "reword the thing", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}

	p := decodeShip(t, res)
	if !p.Updated || p.URL != prURL {
		t.Errorf("payload = %+v, want updated with the existing URL", p)
	}
	if _, edited := editArgv(t, log); !edited {
		t.Fatal("gh pr edit was never called")
	}
	if body := editedBody(t, editBody); !strings.Contains(body, "generated body") {
		t.Errorf("edited body = %q, want the generated prose", body)
	}
}

// U2 — --update --body-file is verbatim and costs no provider call, the same
// asymmetry the create path has.
func TestUpdate_BodyFileIsVerbatimAndSkipsTheProvider(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	sentinel := sentinelProvider(t, repo)
	_, editBody := withExistingPR(t, repo)

	writeFile(t, repo, "body.md", "## Description\nwritten by hand\n")

	res := run(t, repo, "pr", "--update", "--body-file", "body.md", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}

	if body := editedBody(t, editBody); body != "## Description\nwritten by hand\n" {
		t.Errorf("edited body = %q, want it verbatim", body)
	}
	if exists(sentinel) {
		t.Error("the provider was invoked for a body that was supplied")
	}
}

// U3 — prose without --update is an error naming the flag, not a silent drop.
func TestUpdate_ProseWithoutTheFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"intent", []string{"--intent", "reword the thing"}},
		{"body", []string{"--body", "## Description\nhand written\n"}},
		{"title", []string{"--title", "feat(x): a better subject"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			withRemote(t, repo)
			onFeatureBranch(t, repo)
			log, _ := withExistingPR(t, repo)

			res := run(t, repo, append([]string{"pr", "--json"}, tc.args...)...)
			if res.exitCode == 0 {
				t.Fatalf("exit = 0, want an error (stdout %q)", res.stdout)
			}

			p := res.payload(t)
			if !strings.Contains(p.Hint, "--update") {
				t.Errorf("hint = %q, want it to name --update", p.Hint)
			}
			if argv, edited := editArgv(t, log); edited {
				t.Errorf("gh pr edit ran anyway: %q", argv)
			}
		})
	}
}

// U4 — a bare re-run still converges. The whole point of the flag is that
// nothing changes without it.
func TestUpdate_BareRerunStillConverges(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	log, _ := withExistingPR(t, repo)

	res := run(t, repo, "pr", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", res.exitCode, res.stderr)
	}

	p := decodeShip(t, res)
	if !p.Existing || p.Updated || p.URL != prURL {
		t.Errorf("payload = %+v, want the existing URL and no update", p)
	}
	if argv, edited := editArgv(t, log); edited {
		t.Errorf("gh pr edit ran on a bare re-run: %q", argv)
	}
}

// U5 — the title is sent only when --title asked for one. Asserted on the argv
// rather than the outcome: a title silently rewritten to the commit subject
// would look identical from outside.
func TestUpdate_TitleIsSentOnlyWhenNamed(t *testing.T) {
	t.Run("named", func(t *testing.T) {
		repo := newRepo(t)
		withRemote(t, repo)
		onFeatureBranch(t, repo)
		sentinelProvider(t, repo)
		log, _ := withExistingPR(t, repo)

		if res := run(t, repo, "pr", "--update", "--title", "feat(x): a better subject"); res.exitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.exitCode, res.stderr)
		}

		argv, edited := editArgv(t, log)
		if !edited {
			t.Fatal("gh pr edit was never called")
		}
		if !strings.Contains(argv, "--title feat(x): a better subject") {
			t.Errorf("argv = %q, want it to carry the new title", argv)
		}
	})

	t.Run("not named", func(t *testing.T) {
		repo := newRepo(t)
		withRemote(t, repo)
		onFeatureBranch(t, repo)
		sentinelProvider(t, repo)
		log, _ := withExistingPR(t, repo)

		if res := run(t, repo, "pr", "--update"); res.exitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.exitCode, res.stderr)
		}

		argv, edited := editArgv(t, log)
		if !edited {
			t.Fatal("gh pr edit was never called")
		}
		if strings.Contains(argv, "--title") {
			t.Errorf("argv = %q, want no --title for a body-only update", argv)
		}
	})
}

// U6 — a preview writes nothing and generates nothing, and still names the
// pull request it would rewrite.
func TestUpdate_DryRunEditsNothingAndCallsNoProvider(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	sentinel := sentinelProvider(t, repo)
	log, _ := withExistingPR(t, repo)

	res := run(t, repo, "pr", "--dry-run", "--update", "--intent", "reword the thing", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}

	p := decodeShip(t, res)
	if !p.DryRun || !p.Updated || p.URL != prURL {
		t.Errorf("payload = %+v, want a dry-run naming the pull request it would update", p)
	}
	if argv, edited := editArgv(t, log); edited {
		t.Errorf("gh pr edit ran under --dry-run: %q", argv)
	}
	if exists(sentinel) {
		t.Error("the provider was invoked under --dry-run")
	}
}
