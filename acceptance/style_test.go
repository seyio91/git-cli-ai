package acceptance

import (
	"strings"
	"testing"
)

// SC-19 — `commit` honours the resolved commit.style.
//
// These exist because SC-10..SC-13 asserted only on what `config` prints. A
// value can resolve correctly and still reach no consumer, which is exactly the
// defect this criterion covers: the style is validated against the message, not
// merely reported.

// SC-19 — a repo-configured style changes which messages commit accepts.
func TestSC19_CommitAcceptsGitmojiMessageUnderGitmojiStyle(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", ":sparkles: add a thing", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 under gitmoji style (stdout: %s stderr: %s)", res.exitCode, res.stdout, res.stderr)
	}
	if got := commitCount(t, repo); got != "2" {
		t.Fatalf("commit count = %s, want 2", got)
	}
	if got := commitMessage(t, repo); got != ":sparkles: add a thing\n" {
		t.Fatalf("stored message = %q, want the supplied message verbatim", got)
	}
}

// SC-19 — the same message must still be rejected under the default style, or
// the test above would pass for the wrong reason (a validator that accepts
// everything).
func TestSC19_CommitRejectsGitmojiMessageUnderDefaultStyle(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", ":sparkles: add a thing", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for a gitmoji message under conventional-commits")
	}
	if got := commitCount(t, repo); got != "1" {
		t.Fatalf("commit count = %s, want 1 (nothing committed)", got)
	}
	if p := res.payload(t); !strings.Contains(p.Error, "conventional-commits") {
		t.Fatalf("error should name the active style: %q", p.Error)
	}
}

// SC-19 — a conventional message is rejected under gitmoji, proving the switch
// runs in both directions.
func TestSC19_CommitRejectsNonConformingMessageUnderGitmojiStyle(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", "feat(x): add a thing", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for a message with no emoji under gitmoji")
	}
	if p := res.payload(t); !strings.Contains(p.Error, "gitmoji") {
		t.Fatalf("error should name the active style: %q", p.Error)
	}
}

// SC-19 — gitmoji accepts a literal emoji and an optional conventional prefix
// after the emoji token.
func TestSC19_GitmojiAcceptsLiteralEmojiAndConventionalPrefix(t *testing.T) {
	for _, message := range []string{"✨ add a thing", ":sparkles: feat(api): add a thing"} {
		t.Run(message, func(t *testing.T) {
			repo := newRepo(t)
			writeRepoConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")
			writeFile(t, repo, "a.txt", "a\n")
			mustGit(t, repo, "add", "a.txt")

			res := run(t, repo, "commit", "--message", message, "--json")
			if res.exitCode != 0 {
				t.Fatalf("exit = %d, want 0 for %q (stdout: %s)", res.exitCode, message, res.stdout)
			}
		})
	}
}

// SC-19 — the style is read from the resolved config, so the global layer
// reaches the commit path too, not just a repo file.
func TestSC19_GlobalStyleReachesCommit(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", ":bug: fix a thing", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 under a globally-configured style (stdout: %s)", res.exitCode, res.stdout)
	}
}

// SC-19 — freeform-with-rules accepts prose the other styles reject, but still
// enforces its own rules.
func TestSC19_FreeformStyleAcceptsProseAndEnforcesRules(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstyle = \"freeform-with-rules\"\n")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", "make the thing work again", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 under freeform-with-rules (stdout: %s)", res.exitCode, res.stdout)
	}

	writeFile(t, repo, "b.txt", "b\n")
	mustGit(t, repo, "add", "b.txt")

	res = run(t, repo, "commit", "--message", strings.Repeat("x", 73), "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for a header longer than 72 characters")
	}

	res = run(t, repo, "commit", "--message", "make the thing work again.", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for a header ending in a period")
	}
}

// SC-19 — a multi-line message keeps the strict-header/opaque-body rule under
// every style, not only under conventional-commits.
func TestSC19_BodyRuleAppliesUnderEveryStyle(t *testing.T) {
	for style, header := range map[string]string{
		"gitmoji":             ":sparkles: add a thing",
		"freeform-with-rules": "add a thing",
	} {
		t.Run(style, func(t *testing.T) {
			repo := newRepo(t)
			writeRepoConfig(t, repo, "[commit]\nstyle = \""+style+"\"\n")
			writeFile(t, repo, "a.txt", "a\n")
			mustGit(t, repo, "add", "a.txt")

			res := run(t, repo, "commit", "--message", header+"\n\nA body explaining why.", "--json")
			if res.exitCode != 0 {
				t.Fatalf("exit = %d, want 0 for a well-formed body (stdout: %s)", res.exitCode, res.stdout)
			}

			writeFile(t, repo, "b.txt", "b\n")
			mustGit(t, repo, "add", "b.txt")

			res = run(t, repo, "commit", "--message", header+"\nno blank line", "--json")
			if res.exitCode == 0 {
				t.Fatal("expected nonzero exit for a body with no blank line after the header")
			}
		})
	}
}
