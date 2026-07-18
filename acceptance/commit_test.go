package acceptance

import (
	"reflect"
	"strings"
	"testing"
)

// SC-07 — a conforming --message is used verbatim, with no rewriting.
func TestSC07_ConformingMessageIsUsedVerbatim(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	// Internal runs of spaces, not leading ones: a subject starting with extra
	// whitespace is correctly rejected by the validator (see SC-08).
	message := "feat(cli): keep   internal  spacing"
	res := run(t, repo, "commit", "--message", message, "--json")

	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := strings.TrimRight(commitMessage(t, repo), "\n"); got != message {
		t.Fatalf("stored message = %q, want %q", got, message)
	}
	if got := res.payload(t).Message; got != message {
		t.Fatalf("payload message = %q, want %q", got, message)
	}
}

// SC-01 — an optional body survives byte-for-byte after a blank line.
//
// Note: git's default -m cleanup collapses *consecutive* empty lines and trims
// trailing whitespace. Single blank separators, as used here, are preserved, so
// any difference this test catches is ours and not git's.
func TestSC01_BodyAndFooterRoundTripVerbatim(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	message := "feat(api)!: drop v1 endpoints\n\nRewrote the routing layer.\n\nBREAKING CHANGE: v1 removed.\nRefs: TASK-412"
	res := run(t, repo, "commit", "--message", message, "--json")

	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := strings.TrimRight(commitMessage(t, repo), "\n"); got != message {
		t.Fatalf("stored message = %q, want %q", got, message)
	}
	if got := strings.TrimSpace(mustGit(t, repo, "log", "-1", "--format=%s")); got != "feat(api)!: drop v1 endpoints" {
		t.Fatalf("subject line = %q, want header only", got)
	}
}

// SC-01 — a body not preceded by a blank line is rejected.
func TestSC01_BodyWithoutBlankLineIsRejected(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	before := commitCount(t, repo)
	res := run(t, repo, "commit", "--message", "feat(api): drop v1\nbody", "--json")

	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit")
	}
	if got := res.payload(t).Error; !strings.Contains(got, "blank line") {
		t.Fatalf("error = %q, want it to name the blank-line rule", got)
	}
	if after := commitCount(t, repo); after != before {
		t.Fatalf("commit count changed %s -> %s", before, after)
	}
}

// SC-08 — a non-conforming message errors, naming what failed and how to fix it.
func TestSC08_NonConformingMessageErrorsWithGuidance(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	before := commitCount(t, repo)
	res := run(t, repo, "commit", "--message", "just some words", "--json")

	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit")
	}
	p := res.payload(t)
	if !strings.Contains(p.Error, "Conventional Commits") {
		t.Fatalf("error = %q, want it to name the format", p.Error)
	}
	if p.Hint == "" {
		t.Fatal("expected a hint explaining how to fix it")
	}
	if after := commitCount(t, repo); after != before {
		t.Fatalf("an unvalidated message reached a commit: %s -> %s", before, after)
	}
}

// SC-03 — an empty stage errors and lists the unstaged changes.
func TestSC03_EmptyStageListsUnstagedChanges(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "seed.txt", "seed\nmodified\n")

	res := run(t, repo, "commit", "--message", "feat(x): thing", "--json")

	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit")
	}
	p := res.payload(t)
	if !strings.Contains(p.Error, "nothing staged") {
		t.Fatalf("error = %q, want it to say nothing is staged", p.Error)
	}
	if !strings.Contains(p.Hint, "seed.txt") {
		t.Fatalf("hint = %q, want it to list the unstaged file", p.Hint)
	}
}

// SC-03 — when only untracked files exist, the hint says they are never staged
// implicitly rather than suggesting --all, which would not stage them.
func TestSC03_UntrackedOnlyHintDoesNotPromiseAll(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "brand-new.txt", "new\n")

	res := run(t, repo, "commit", "--message", "feat(x): thing", "--json")

	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit")
	}
	hint := res.payload(t).Hint
	if !strings.Contains(hint, "brand-new.txt") {
		t.Fatalf("hint = %q, want it to list the untracked file", hint)
	}
	if !strings.Contains(hint, "never staged implicitly") {
		t.Fatalf("hint = %q, want it to explain untracked files are not swept in", hint)
	}
}

// SC-04 — --all stages tracked modified and deleted files, never untracked ones.
func TestSC04_AllStagesTrackedOnly(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "tracked.txt", "tracked\n")
	writeFile(t, repo, "doomed.txt", "doomed\n")
	mustGit(t, repo, "add", "tracked.txt", "doomed.txt")
	mustGit(t, repo, "commit", "-qm", "chore: baseline")

	// A tracked file modified in the worktree, a tracked file deleted from the
	// worktree, and a file git has never seen. --all must pick up the first two
	// and ignore the third.
	writeFile(t, repo, "tracked.txt", "tracked\nmodified\n")
	removeFile(t, repo, "doomed.txt")
	writeFile(t, repo, "untracked.txt", "untracked\n")

	res := run(t, repo, "commit", "--message", "fix(y): sweep tracked", "--all", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}

	committed := committedFiles(t, repo)
	for _, file := range committed {
		if file == "untracked.txt" {
			t.Fatalf("--all swept an untracked file: %v", committed)
		}
	}
	if !contains(committed, "tracked.txt") {
		t.Fatalf("committed = %v, want it to include the modified tracked file", committed)
	}
	if !contains(committed, "doomed.txt") {
		t.Fatalf("committed = %v, want it to include the deleted tracked file", committed)
	}
	if got := untrackedFiles(t, repo); !reflect.DeepEqual(got, []string{"untracked.txt"}) {
		t.Fatalf("untracked after commit = %v, want it left alone", got)
	}
}

// SC-05 — --dry-run mutates nothing.
func TestSC05_DryRunMutatesNothing(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	before := commitCount(t, repo)
	beforeStatus := mustGit(t, repo, "status", "--porcelain=v1")

	res := run(t, repo, "commit", "--message", "feat(x): thing", "--dry-run", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if !res.payload(t).DryRun {
		t.Fatal("payload should carry dry_run: true")
	}
	if after := commitCount(t, repo); after != before {
		t.Fatalf("commit count changed %s -> %s", before, after)
	}
	if after := mustGit(t, repo, "status", "--porcelain=v1"); after != beforeStatus {
		t.Fatalf("working tree changed:\nbefore %q\nafter  %q", beforeStatus, after)
	}
}

// SC-05 — --dry-run with --all must not stage anything either.
func TestSC05_DryRunWithAllDoesNotStage(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "seed.txt", "seed\nmodified\n")

	beforeStatus := mustGit(t, repo, "status", "--porcelain=v1")
	res := run(t, repo, "commit", "--message", "feat(x): thing", "--all", "--dry-run", "--json")

	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if after := mustGit(t, repo, "status", "--porcelain=v1"); after != beforeStatus {
		t.Fatalf("--dry-run --all staged something:\nbefore %q\nafter  %q", beforeStatus, after)
	}
}

// SC-06 — under --json every failure class emits {error,hint} and exits nonzero.
func TestSC06_JSONFailuresAreStructuredAndExitNonzero(t *testing.T) {
	tests := map[string]struct {
		setup func(t *testing.T, repo string)
		args  []string
	}{
		"validation failure": {
			setup: func(t *testing.T, repo string) {
				writeFile(t, repo, "a.txt", "a\n")
				mustGit(t, repo, "add", "a.txt")
			},
			args: []string{"commit", "--message", "nope", "--json"},
		},
		"empty stage": {
			setup: func(t *testing.T, repo string) {},
			args:  []string{"commit", "--message", "feat(x): thing", "--json"},
		},
		"missing message": {
			setup: func(t *testing.T, repo string) {},
			args:  []string{"commit", "--json"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			tt.setup(t, repo)

			res := run(t, repo, tt.args...)
			if res.exitCode == 0 {
				t.Fatalf("exit = 0, want nonzero (stdout: %s)", res.stdout)
			}
			p := res.payload(t)
			if p.Error == "" {
				t.Fatalf("expected an error field, got %q", res.stdout)
			}
			if p.Hint == "" {
				t.Fatalf("expected a hint field, got %q", res.stdout)
			}
		})
	}
}

// SC-06 — a git-level failure is also reported as structured JSON.
func TestSC06_GitFailureIsStructured(t *testing.T) {
	dir := t.TempDir() // deliberately not a git repository

	res := run(t, dir, "commit", "--message", "feat(x): thing", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit outside a git repository")
	}
	if p := res.payload(t); p.Error == "" {
		t.Fatalf("expected structured error, got %q", res.stdout)
	}
}

// SC-02 — pathological filenames survive verbatim.
func TestSC02_PathologicalPathsSurviveVerbatim(t *testing.T) {
	repo := newRepo(t)
	for _, name := range []string{"weird -> name.txt", "café thing.txt", "spaced  out.txt"} {
		writeFile(t, repo, name, "x\n")
	}
	mustGit(t, repo, "add", "-A")

	res := run(t, repo, "commit", "--message", "feat(fs): add odd paths", "--dry-run", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}

	want := []string{"café thing.txt", "spaced  out.txt", "weird -> name.txt"}
	if got := res.payload(t).Files; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

// SC-02 — a rename reports the new path, and does not swallow a sibling entry.
func TestSC02_RenameReportsNewPathWithoutSwallowingSiblings(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "original.txt", "content\n")
	mustGit(t, repo, "add", "original.txt")
	mustGit(t, repo, "commit", "-qm", "chore: add original")

	mustGit(t, repo, "mv", "original.txt", "renamed.txt")
	writeFile(t, repo, "sibling.txt", "sibling\n")
	mustGit(t, repo, "add", "sibling.txt")

	res := run(t, repo, "commit", "--message", "refactor(fs): rename", "--dry-run", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}

	files := res.payload(t).Files
	if !contains(files, "renamed.txt") {
		t.Fatalf("files = %#v, want the post-rename path", files)
	}
	if contains(files, "original.txt") {
		t.Fatalf("files = %#v, should not report the pre-rename path", files)
	}
	if !contains(files, "sibling.txt") {
		t.Fatalf("files = %#v, rename record swallowed the sibling entry", files)
	}
}

// SC-09 — global flags are reachable in either position relative to the subcommand.
func TestSC09_GlobalFlagsWorkInEitherPosition(t *testing.T) {
	for _, args := range [][]string{
		{"commit", "--message", "feat(x): thing", "--dry-run", "--json"},
		{"--json", "--dry-run", "commit", "--message", "feat(x): thing"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			repo := newRepo(t)
			writeFile(t, repo, "a.txt", "a\n")
			mustGit(t, repo, "add", "a.txt")

			res := run(t, repo, args...)
			if res.exitCode != 0 {
				t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
			}
			if !res.payload(t).DryRun {
				t.Fatal("expected dry_run to be honoured")
			}
		})
	}
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
