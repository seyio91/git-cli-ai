package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

// Criteria for `commit.types`, the opt-in closed type vocabulary. The
// conventional validator has always checked the shape of a type and never its
// name, so a generated message could invent one — and because branch.pattern
// is {type}/{slug}, an invented type became a branch namespace.

// configList reads a list-valued config key out of the JSON payload.
func configList(t *testing.T, r result, path string) []string {
	t.Helper()

	var current any = decodeConfig(t, r).Config
	for _, segment := range strings.Split(path, ".") {
		block, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("config path %q: %q is not a table", path, segment)
		}
		current, ok = block[segment]
		if !ok {
			return nil // omitempty: an unset list is absent, not null
		}
	}

	raw, ok := current.([]any)
	if !ok {
		t.Fatalf("config path %q is not a list: %#v", path, current)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(string))
	}
	return out
}

// T1 — a vocabulary that cannot do its job is refused at load, naming the key
// and the file. An empty list is the interesting one: reading it as "any type"
// would be the widest possible reading of the narrowest possible instruction.
func TestCommitTypes_InvalidVocabularyIsRejectedAtLoad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		names string
	}{
		{"empty list", "[]", "empty"},
		{"capitalised", `["Feat"]`, "Feat"},
		{"trailing bang", `["feat!"]`, "feat!"},
		{"duplicate", `["feat", "feat"]`, "feat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			writeRepoConfig(t, repo, "[commit]\ntypes = "+tc.value+"\n")

			res := run(t, repo, "config", "--json")
			if res.exitCode == 0 {
				t.Fatalf("exit = 0, want an error (stdout %q)", res.stdout)
			}

			p := res.payload(t)
			if !strings.Contains(p.Error, "commit.types") {
				t.Errorf("error = %q, want it to name the key", p.Error)
			}
			if !strings.Contains(p.Error, ".git-cli.toml") {
				t.Errorf("error = %q, want it to name the file", p.Error)
			}
			if !strings.Contains(p.Error, tc.names) {
				t.Errorf("error = %q, want it to name %q", p.Error, tc.names)
			}
			if !strings.Contains(p.Hint, "remove the key") {
				t.Errorf("hint = %q, want it to say how to get the open vocabulary back", p.Hint)
			}
		})
	}
}

// vocabularyRepo is a repo with a staged change, a three-type vocabulary, and
// a fake provider. The provider is present in every case so that "no provider
// call" is a real assertion rather than a vacuous one.
func vocabularyRepo(t *testing.T, generates string) (repo string, log string) {
	t.Helper()

	repo = newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", generates)
	writeRepoConfig(t, repo, cliProviderConfig(fake)+"\n[commit]\ntypes = [\"feat\", \"fix\", \"chore\"]\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	return repo, log
}

// T3 — a supplied -m that already declares an unlisted type is refused, and
// refused without a provider call. Rendering it would commit a type other than
// the one that was typed.
func TestCommitTypes_SuppliedUnlistedTypeIsRefused(t *testing.T) {
	repo, log := vocabularyRepo(t, "echo 'feat: rewritten by the model'")

	before := commitCount(t, repo)
	res := run(t, repo, "commit", "-m", "wip: half done", "--json")
	if res.exitCode == 0 {
		t.Fatalf("exit = 0, want an error (stdout %q)", res.stdout)
	}

	p := res.payload(t)
	for _, want := range []string{"feat", "fix", "chore"} {
		if !strings.Contains(p.Hint, want) {
			t.Errorf("hint = %q, want it to name the allowed type %q", p.Hint, want)
		}
	}
	if !strings.Contains(p.Error, "wip") {
		t.Errorf("error = %q, want it to name the rejected type", p.Error)
	}
	if calls := providerCalls(t, log); calls != 0 {
		t.Errorf("provider called %d times for a message that was already a statement", calls)
	}
	if after := commitCount(t, repo); after != before {
		t.Errorf("a refused message reached a commit: %s -> %s", before, after)
	}
}

// T4 — a listed type still commits verbatim, with no provider call. Without
// this the test above would pass for a tool that rejected everything.
func TestCommitTypes_SuppliedListedTypeCommitsVerbatim(t *testing.T) {
	repo, log := vocabularyRepo(t, "echo 'feat: rewritten by the model'")

	if res := run(t, repo, "commit", "-m", "fix: a real fix", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "fix: a real fix") {
		t.Errorf("stored message = %q, want it verbatim", got)
	}
	if calls := providerCalls(t, log); calls != 0 {
		t.Errorf("provider called %d times for a conforming message", calls)
	}
}

// T5 — the vocabulary must not turn intent into an error. `fixed the thing` is
// not a conventional commit at all, so it is still text to be written up.
func TestCommitTypes_FreeformMessageStillRenders(t *testing.T) {
	repo, log := vocabularyRepo(t, "echo 'fix: correct the thing'")

	if res := run(t, repo, "commit", "-m", "fixed the thing", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "fix: correct the thing") {
		t.Errorf("stored message = %q, want the rendered message", got)
	}
	if calls := providerCalls(t, log); calls != 1 {
		t.Errorf("provider called %d times, want exactly 1", calls)
	}
}

// T6 — a *generated* unlisted type is corrected rather than fatal: the
// existing one-retry-with-feedback loop carries the rejection back, so no new
// machinery was needed for the generated path.
func TestCommitTypes_GeneratedUnlistedTypeIsCorrectedOnRetry(t *testing.T) {
	repo := newRepo(t)
	marker := filepath.Join(repo, ".git", "generated-once")
	fake, log := writeFakeProvider(t, repo, "fake-provider",
		"if [ -f "+marker+" ]; then echo 'feat: add the thing'; else touch "+marker+"; echo 'ai: add the thing'; fi")
	writeRepoConfig(t, repo, cliProviderConfig(fake)+"\n[commit]\ntypes = [\"feat\", \"fix\", \"chore\"]\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	if res := run(t, repo, "commit", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "feat: add the thing") {
		t.Errorf("stored message = %q, want the corrected message", got)
	}
	if calls := providerCalls(t, log); calls != 2 {
		t.Errorf("provider called %d times, want 2 — one rejection and one correction", calls)
	}
	if prompts := providerPrompts(t, log); !strings.Contains(prompts, "feat, fix, chore") {
		t.Error("the prompt never stated the vocabulary, so the model could only learn it by being rejected")
	}
}

// T7 — with the vocabulary unset, everything above reverts to today's
// behaviour: an invented type is accepted, verbatim, with no provider call.
// This is the guarantee that makes the option opt-in.
func TestCommitTypes_UnsetVocabularyAcceptsAnInventedType(t *testing.T) {
	repo := newRepo(t)
	fake, log := writeFakeProvider(t, repo, "fake-provider", "echo 'feat: rewritten by the model'")
	writeRepoConfig(t, repo, cliProviderConfig(fake))
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	if res := run(t, repo, "commit", "-m", "wip: half done", "--json"); res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if got := commitMessage(t, repo); !strings.HasPrefix(got, "wip: half done") {
		t.Errorf("stored message = %q, want it verbatim", got)
	}
	if calls := providerCalls(t, log); calls != 0 {
		t.Errorf("provider called %d times for a shape-valid message", calls)
	}
}

// T2 — config reports the vocabulary and which layer set it, and reports the
// unset case as the default rather than omitting the key.
func TestCommitTypes_ConfigReportsTheVocabulary(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		repo := newRepo(t)
		writeRepoConfig(t, repo, "[commit]\ntypes = [\"feat\", \"fix\", \"chore\"]\n")

		res := run(t, repo, "config", "--json")
		if res.exitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.exitCode, res.stderr)
		}

		got := configList(t, res, "commit.types")
		if strings.Join(got, ",") != "feat,fix,chore" {
			t.Errorf("commit.types = %v, want [feat fix chore]", got)
		}
		if layer := configSource(t, res, "commit.types"); layer != "repo" {
			t.Errorf("commit.types provenance = %q, want repo", layer)
		}
	})

	t.Run("unset", func(t *testing.T) {
		repo := newRepo(t)

		res := run(t, repo, "config", "--json")
		if res.exitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.exitCode, res.stderr)
		}

		if got := configList(t, res, "commit.types"); len(got) != 0 {
			t.Errorf("commit.types = %v, want empty when unset", got)
		}
		if layer := configSource(t, res, "commit.types"); layer != "default" {
			t.Errorf("commit.types provenance = %q, want default", layer)
		}

		// The human output must still carry the line: "any type is accepted"
		// is a state of the setting, not the absence of one.
		plain := run(t, repo, "config")
		if !strings.Contains(plain.stdout, "commit.types = [] (default)") {
			t.Errorf("config output does not report an unset vocabulary:\n%s", plain.stdout)
		}
	})
}
