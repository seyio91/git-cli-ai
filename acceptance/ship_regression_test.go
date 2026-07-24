package acceptance

import (
	"strings"
	"testing"
)

// Regressions found by cross-model validation of the first ship implementation.
// Each reproduces a defect on a path SC-18a..h did not exercise.

// pinDefaultWithPattern pins the default branch and a custom branch.pattern.
func pinDefaultWithPattern(t *testing.T, repo string, pattern string) string {
	t.Helper()
	def := currentBranch(t, repo)
	writeFile(t, repo, ".git-cli.toml",
		"[branch]\ndefault_branch = \""+def+"\"\npattern = \""+pattern+"\"\n")
	mustGit(t, repo, "add", ".git-cli.toml")
	mustGit(t, repo, "commit", "-qm", "chore: pin default and pattern")
	return def
}

// Finding 1 (HIGH) — a derived branch name equal to the default branch must
// never be committed to. With pattern "{slug}" and a subject slugging to the
// default's name, the naive reuse-if-ancestor rule would check out the default
// (its tip is trivially an ancestor of HEAD) and commit onto it.
func TestShipNeverCommitsToDefaultViaCollision(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	def := pinDefaultWithPattern(t, repo, "{slug}")
	defHead := strings.TrimSpace(mustGit(t, repo, "rev-parse", def))

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	writeFakeGH(t, repo, ghCreateSucceeds)

	// Subject slugs to exactly the default branch name.
	ship := decodeShip(t, run(t, repo, "ship", "-m", "feat: "+def, "--json"))

	if ship.Branch == def {
		t.Fatalf("shipped onto the default branch %q", def)
	}
	if after := strings.TrimSpace(mustGit(t, repo, "rev-parse", def)); after != defHead {
		t.Errorf("default branch %s moved %s -> %s", def, defHead, after)
	}
	if now := currentBranch(t, repo); now == def {
		t.Errorf("repo is on the default branch %q after ship", def)
	}
}

// Finding 2 (HIGH) — when the current branch is the only local branch and the
// base has no local ref, ship must not treat an empty branch as a resume and
// open a duplicate PR. With no open PR, this is the genuine-empty case.
func TestShipSingleBranchNoBaseIsNotAFalseResume(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	// feat/only becomes the sole local branch, pointing at the seed — no work
	// of its own. The base (gh reports "main") has no local ref.
	mustGit(t, repo, "checkout", "-q", "-b", "feat/only")
	mustGit(t, repo, "branch", "-D", currentBranchOther(t, repo, "feat/only"))
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	res := run(t, repo, "ship", "--json")

	calls := strings.Join(ghCalls(t, log), "\n")
	if strings.Contains(calls, "pr create") {
		t.Errorf("opened a PR for an empty single-branch repo; gh calls:\n%s", calls)
	}
	if res.exitCode == 0 {
		t.Errorf("exit = 0, want the empty-stage error")
	}
}

// currentBranchOther returns the first local branch that is not exclude.
func currentBranchOther(t *testing.T, repo string, exclude string) string {
	t.Helper()
	for _, b := range strings.Split(strings.TrimSpace(mustGit(t, repo, "branch", "--format=%(refname:short)")), "\n") {
		if b = strings.TrimSpace(b); b != "" && b != exclude {
			return b
		}
	}
	t.Fatal("no other branch to delete")
	return ""
}

// Finding 3 (MEDIUM) — the suffix search must reuse a suffixed branch that is
// our own earlier partial run, not skip past it and leak another branch.
func TestShipReusesSuffixedAncestorBranch(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	pinDefaultBranch(t, repo)

	// feat/add-a-thing: unrelated work (not an ancestor), so the base name must
	// be skipped.
	mustGit(t, repo, "checkout", "-q", "-b", "feat/add-a-thing")
	writeFile(t, repo, "other.txt", "other\n")
	mustGit(t, repo, "add", "other.txt")
	mustGit(t, repo, "commit", "-qm", "feat: unrelated")
	mustGit(t, repo, "checkout", "-q", "-")
	// feat/add-a-thing-2: our own earlier partial run, an ancestor of HEAD.
	mustGit(t, repo, "branch", "feat/add-a-thing-2")

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	writeFakeGH(t, repo, ghCreateSucceeds)

	ship := decodeShip(t, run(t, repo, "ship", "-m", "feat: add a thing", "--json"))
	if ship.Branch != "feat/add-a-thing-2" {
		t.Errorf("branch = %q, want the reused feat/add-a-thing-2 (not a fresh -3)", ship.Branch)
	}
}

// Finding 4 (MEDIUM) — created_branch must be false when the branch was reused,
// not created.
func TestShipCreatedBranchFalseOnReuse(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	pinDefaultBranch(t, repo)
	mustGit(t, repo, "branch", "feat/add-a-thing") // at HEAD → reused

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	writeFakeGH(t, repo, ghCreateSucceeds)

	ship := decodeShip(t, run(t, repo, "ship", "-m", "feat: add a thing", "--json"))
	if ship.Branch != "feat/add-a-thing" {
		t.Fatalf("branch = %q, want the reused feat/add-a-thing", ship.Branch)
	}
	if ship.CreatedBranch {
		t.Errorf("created_branch = true for a reused branch; nothing was created")
	}
}

// Finding 5 (MEDIUM) — a gitmoji message with an embedded conventional prefix
// must slug from the description, not repeat the type and scope.
func TestShipGitmojiBranchName(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	def := currentBranch(t, repo)
	writeFile(t, repo, ".git-cli.toml",
		"[commit]\nstyle = \"gitmoji\"\n\n[branch]\ndefault_branch = \""+def+"\"\n")
	mustGit(t, repo, "add", ".git-cli.toml")
	mustGit(t, repo, "commit", "-qm", "chore: gitmoji")

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	writeFakeGH(t, repo, ghCreateSucceeds)

	ship := decodeShip(t, run(t, repo, "ship", "-m", ":sparkles: feat(api): add endpoint", "--json"))
	if ship.Branch != "feat/add-endpoint" {
		t.Errorf("branch = %q, want feat/add-endpoint (type from prefix, slug from description)", ship.Branch)
	}
}
