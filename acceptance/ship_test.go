package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Phase 6 criteria for `ship`. Written before the command exists and expected
// to fail until it lands. `ship` composes the commit and pr flows, so these
// assert the seams between them — branch creation on the default branch,
// resume, collision — not the pieces already covered by SC-01..29.

type shipResult struct {
	Message       string `json:"message"`
	Branch        string `json:"branch"`
	Base          string `json:"base"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	CreatedBranch bool   `json:"created_branch"`
	Existing      bool   `json:"existing"`
	Updated       bool   `json:"updated"`
	Draft         bool   `json:"draft"`
	Pushed        bool   `json:"pushed"`
	DryRun        bool   `json:"dry_run"`
	Error         string `json:"error"`
	Hint          string `json:"hint"`

	Completed []string `json:"completed"`
	Resume    string   `json:"resume"`
}

func decodeShip(t *testing.T, r result) shipResult {
	t.Helper()
	var s shipResult
	if err := json.Unmarshal([]byte(r.stdout), &s); err != nil {
		t.Fatalf("stdout is not a ship payload (%v): %q", err, r.stdout)
	}
	return s
}

// currentBranch is the branch the repo is on, read straight from git.
func currentBranch(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(mustGit(t, repo, "branch", "--show-current"))
}

// pinDefaultBranch makes the tool treat the repo's current branch as the
// default, so a ship run from it exercises the on-default path deterministically
// regardless of whether git initialised master or main.
func pinDefaultBranch(t *testing.T, repo string) string {
	t.Helper()
	def := currentBranch(t, repo)
	writeFile(t, repo, ".git-cli.toml", "[branch]\ndefault_branch = \""+def+"\"\n")
	mustGit(t, repo, "add", ".git-cli.toml")
	mustGit(t, repo, "commit", "-qm", "chore: pin default branch")
	return def
}

// SC-18a — on a feature branch with staged changes, ship stages, commits,
// pushes and opens a PR, returning the URL.
func TestSC18a_FeatureBranchCommitsPushesAndOpensPR(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	mustGit(t, repo, "checkout", "-q", "-b", "feat/thing")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	res := run(t, repo, "ship", "-m", "feat(x): add a thing", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d (stdout %q, stderr %q)", res.exitCode, res.stdout, res.stderr)
	}

	ship := decodeShip(t, res)
	if ship.URL != prURL {
		t.Errorf("url = %q, want %q", ship.URL, prURL)
	}
	if ship.Branch != "feat/thing" {
		t.Errorf("branch = %q, want feat/thing", ship.Branch)
	}
	if ship.CreatedBranch {
		t.Errorf("created_branch = true, but the run was already on a feature branch")
	}
	// seed (1) + the one commit ship makes (2). A third would be a stray commit.
	if got := commitCount(t, repo); got != "2" {
		t.Errorf("commit count = %s, want 2 (seed + one ship commit)", got)
	}
	if !strings.Contains(strings.Join(ghCalls(t, log), "\n"), "pr create") {
		t.Errorf("gh pr create was not invoked")
	}
}

// SC-18b — on the default branch, ship creates a branch from the resolved
// message before committing, and never commits to the default branch itself.
func TestSC18b_DefaultBranchCreatesBranchBeforeCommitting(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	def := pinDefaultBranch(t, repo)
	defHead := strings.TrimSpace(mustGit(t, repo, "rev-parse", def))

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	res := run(t, repo, "ship", "-m", "feat(x): add a thing", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d (stdout %q, stderr %q)", res.exitCode, res.stdout, res.stderr)
	}

	ship := decodeShip(t, res)
	if !ship.CreatedBranch {
		t.Errorf("created_branch = false, want true on the default branch")
	}
	if ship.Branch == def || ship.Branch == "" {
		t.Errorf("branch = %q, want a derived feature branch", ship.Branch)
	}
	if !strings.Contains(ship.Branch, "add-a-thing") {
		t.Errorf("branch = %q, want it derived from the message", ship.Branch)
	}
	if now := currentBranch(t, repo); now != ship.Branch {
		t.Errorf("repo is on %q, want the created branch %q", now, ship.Branch)
	}
	// The default branch must not have moved: nothing is ever committed to it.
	if after := strings.TrimSpace(mustGit(t, repo, "rev-parse", def)); after != defHead {
		t.Errorf("default branch %s moved from %s to %s", def, defHead, after)
	}
	if !strings.Contains(strings.Join(ghCalls(t, log), "\n"), "pr create") {
		t.Errorf("gh pr create was not invoked")
	}
}

// SC-35 — ship is not atomic, and a failure after the push has to say so. The
// branch, the commit and the push have all landed by the time the forge is
// asked for a pull request; an error naming only that last step reads like a
// total failure, and a blind retry commits a second time.
func TestSC35_FailureAfterPushReportsWhatLanded(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	def := pinDefaultBranch(t, repo)

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	// pr create is the step that fails; everything before it succeeds, which is
	// exactly the shape of the incident this reports on.
	writeFakeGH(t, repo, `case "$1 $2" in
  "pr list") echo "[]" ;;
  "repo view") echo '{"defaultBranchRef":{"name":"`+def+`"}}' ;;
  "pr create") echo "the forge said no" >&2; exit 1 ;;
  *) echo "unexpected gh invocation: $@" >&2; exit 64 ;;
esac`)

	res := run(t, repo, "ship", "-m", "feat(x): add a thing", "--json")
	if res.exitCode == 0 {
		t.Fatalf("exit = 0, want nonzero when pr create fails (stdout %q)", res.stdout)
	}

	ship := decodeShip(t, res)
	if ship.Error == "" {
		t.Errorf("the underlying failure is missing from the payload: %s", res.stdout)
	}
	if len(ship.Completed) == 0 {
		t.Fatalf("no completed steps reported despite a branch, commit and push: %s", res.stdout)
	}

	joined := strings.Join(ship.Completed, "; ")
	for _, want := range []string{"created branch", "committed", "pushed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("completed does not mention %q: %q", want, joined)
		}
	}
	if ship.Resume != "git-cli pr" {
		t.Errorf("resume = %q, want the pull request half of ship", ship.Resume)
	}

	// The work really is on disk and on the remote — the report is not
	// aspirational.
	branch := currentBranch(t, repo)
	if branch == def {
		t.Fatalf("still on %s; the branch was never created", def)
	}
	if out := strings.TrimSpace(mustGit(t, repo, "log", "-1", "--pretty=%s")); out != "feat(x): add a thing" {
		t.Errorf("tip subject = %q, want the committed message", out)
	}
	if remote := strings.TrimSpace(mustGit(t, repo, "rev-parse", "refs/remotes/origin/"+branch)); remote == "" {
		t.Errorf("branch %s is not on the remote despite completed saying it was pushed", branch)
	}
}

// SC-35 — the resume command carries the pull request flags the run was given,
// so following it does not quietly produce a different pull request.
func TestSC35_ResumeCarriesThePullRequestFlags(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	def := pinDefaultBranch(t, repo)

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	writeFakeGH(t, repo, `case "$1 $2" in
  "pr list") echo "[]" ;;
  "repo view") echo '{"defaultBranchRef":{"name":"`+def+`"}}' ;;
  "pr create") echo "the forge said no" >&2; exit 1 ;;
  *) echo "unexpected gh invocation: $@" >&2; exit 64 ;;
esac`)

	res := run(t, repo, "ship", "-m", "feat(x): add a thing",
		"--draft", "--base", def, "--title", "A title: with punctuation", "--body", "## What\nA thing.\n", "--json")
	if res.exitCode == 0 {
		t.Fatalf("exit = 0, want nonzero when pr create fails (stdout %q)", res.stdout)
	}

	resume := decodeShip(t, res).Resume
	for _, want := range []string{"git-cli pr", "--draft", "--base " + def, "--title", "A title: with punctuation", "--body"} {
		if !strings.Contains(resume, want) {
			t.Errorf("resume = %q, want it to carry %q", resume, want)
		}
	}
}

// SC-35 — a failure before anything durable happens carries no partial-progress
// annotation. Reporting an empty resume path on a clean failure would train a
// caller to ignore the field.
func TestSC35_FailureBeforeAnyMutationReportsNoProgress(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	pinDefaultBranch(t, repo)

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	writeFakeGH(t, repo, ghCreateSucceeds)

	// Contradictory body flags fail validation before anything is staged,
	// branched or committed.
	res := run(t, repo, "ship", "-m", "feat(x): add a thing", "--body", "x", "--intent", "y", "--json")
	if res.exitCode == 0 {
		t.Fatal("exit = 0, want nonzero for contradictory body flags")
	}

	ship := decodeShip(t, res)
	if len(ship.Completed) != 0 || ship.Resume != "" {
		t.Errorf("a pre-mutation failure reported progress: completed=%v resume=%q", ship.Completed, ship.Resume)
	}
}

// SC-18c — the message resolves before branch creation: a run that cannot
// resolve a message leaves no branch behind and stays on the default branch.
func TestSC18c_FailedMessageLeavesNoBranch(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	def := pinDefaultBranch(t, repo)

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	writeFakeGH(t, repo, ghCreateSucceeds)

	// No provider configured and no --message: message resolution must fail
	// before anything is created.
	res := run(t, repo, "ship", "--json")
	if res.exitCode == 0 {
		t.Fatalf("exit = 0, want failure when no message can be resolved (stdout %q)", res.stdout)
	}
	if now := currentBranch(t, repo); now != def {
		t.Errorf("repo is on %q, want to remain on %s after a failed resolve", now, def)
	}
	branches := strings.TrimSpace(mustGit(t, repo, "branch", "--format=%(refname:short)"))
	if branches != def {
		t.Errorf("branches = %q, want only %s — a branch was created before the message resolved", branches, def)
	}
}

// SC-18d — nothing staged and nothing to push errors like commit; --all stages
// tracked files and never sweeps in untracked ones.
func TestSC18d_EmptyStageErrorsAndAllStagesTrackedOnly(t *testing.T) {
	t.Run("nothing staged, nothing to push", func(t *testing.T) {
		repo := newRepo(t)
		withRemote(t, repo)
		mustGit(t, repo, "checkout", "-q", "-b", "feat/thing")
		writeFakeGH(t, repo, ghCreateSucceeds)

		res := run(t, repo, "ship", "-m", "feat(x): nothing here", "--json")
		if res.exitCode == 0 {
			t.Fatalf("exit = 0, want the empty-stage error (stdout %q)", res.stdout)
		}
	})

	t.Run("--all stages tracked modified, not untracked", func(t *testing.T) {
		repo := newRepo(t)
		withRemote(t, repo)
		mustGit(t, repo, "checkout", "-q", "-b", "feat/thing")
		writeFile(t, repo, "seed.txt", "seed changed\n") // tracked, modified
		writeFile(t, repo, "new.txt", "new\n")           // untracked
		log := writeFakeGH(t, repo, ghCreateSucceeds)

		res := run(t, repo, "ship", "--all", "-m", "feat(x): change seed", "--json")
		if res.exitCode != 0 {
			t.Fatalf("exit = %d (stdout %q, stderr %q)", res.exitCode, res.stdout, res.stderr)
		}
		files := committedFiles(t, repo)
		if !contains(files, "seed.txt") {
			t.Errorf("committed files %v, want seed.txt staged by --all", files)
		}
		if contains(files, "new.txt") {
			t.Errorf("committed files %v, must not include the untracked new.txt", files)
		}
		_ = log
	})
}

// SC-18e — resume: nothing staged but commits ahead of upstream skips to
// push and PR; a second ship after a successful one is a no-op returning the URL.
func TestSC18e_ResumeAndConverge(t *testing.T) {
	repo := newRepo(t)
	remote := withRemote(t, repo)
	mustGit(t, repo, "checkout", "-q", "-b", "feat/thing")
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	mustGit(t, repo, "commit", "-qm", "feat(x): add a thing")
	// A commit ahead of upstream, nothing staged: the resume state.

	log := writeFakeGH(t, repo, ghCreateSucceeds)

	first := decodeShip(t, run(t, repo, "ship", "--json"))
	if first.URL != prURL {
		t.Fatalf("first ship url = %q, want %q", first.URL, prURL)
	}

	// The branch must now exist on the remote.
	if out := strings.TrimSpace(mustGit(t, remote, "branch", "--format=%(refname:short)")); !strings.Contains(out, "feat/thing") {
		t.Errorf("remote branches = %q, want feat/thing pushed", out)
	}

	// Second run: already open PR is returned, no duplicate create.
	installGH(t, repo, `#!/bin/sh
echo "$@" >> `+log+`
case "$1 $2" in
  "pr list") echo '[{"url":"`+prURL+`"}]' ;;
  "repo view") echo '{"defaultBranchRef":{"name":"main"}}' ;;
  "pr create") echo "MUST NOT CREATE" >&2; exit 65 ;;
esac`)
	second := decodeShip(t, run(t, repo, "ship", "--json"))
	if !second.Existing {
		t.Errorf("second ship existing = false, want true")
	}
	if second.URL != prURL {
		t.Errorf("second ship url = %q, want %q", second.URL, prURL)
	}
}

// SC-18f — branch collision: an existing branch whose tip is an ancestor of
// HEAD is reused; otherwise the name is suffixed.
func TestSC18f_BranchCollision(t *testing.T) {
	t.Run("reuse when existing tip is an ancestor of HEAD", func(t *testing.T) {
		repo := newRepo(t)
		withRemote(t, repo)
		def := pinDefaultBranch(t, repo)
		// A prior branch pointing exactly at HEAD: its tip is an ancestor of
		// HEAD, so it is our own earlier run and must be reused.
		mustGit(t, repo, "branch", "feat/add-a-thing")

		writeFile(t, repo, "a.txt", "a\n")
		mustGit(t, repo, "add", "a.txt")
		writeFakeGH(t, repo, ghCreateSucceeds)

		ship := decodeShip(t, run(t, repo, "ship", "-m", "feat: add a thing", "--json"))
		if ship.Branch != "feat/add-a-thing" {
			t.Errorf("branch = %q, want the reused feat/add-a-thing (no suffix)", ship.Branch)
		}
		_ = def
	})

	t.Run("suffix when existing tip is not an ancestor of HEAD", func(t *testing.T) {
		repo := newRepo(t)
		withRemote(t, repo)
		pinDefaultBranch(t, repo)
		// A prior branch with its own commit HEAD does not have: not an
		// ancestor, so it belongs to unrelated work and must not be reused.
		mustGit(t, repo, "checkout", "-q", "-b", "feat/add-a-thing")
		writeFile(t, repo, "other.txt", "other\n")
		mustGit(t, repo, "add", "other.txt")
		mustGit(t, repo, "commit", "-qm", "feat: unrelated")
		mustGit(t, repo, "checkout", "-q", "-")

		writeFile(t, repo, "a.txt", "a\n")
		mustGit(t, repo, "add", "a.txt")
		writeFakeGH(t, repo, ghCreateSucceeds)

		ship := decodeShip(t, run(t, repo, "ship", "-m", "feat: add a thing", "--json"))
		if ship.Branch != "feat/add-a-thing-2" {
			t.Errorf("branch = %q, want the suffixed feat/add-a-thing-2", ship.Branch)
		}
	})
}

// SC-18g — dry-run previews and mutates nothing.
func TestSC18g_DryRunMutatesNothing(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	def := pinDefaultBranch(t, repo)
	before := commitCount(t, repo)

	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	res := run(t, repo, "ship", "-m", "feat(x): add a thing", "--dry-run", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d (stdout %q, stderr %q)", res.exitCode, res.stdout, res.stderr)
	}

	ship := decodeShip(t, res)
	if !ship.DryRun {
		t.Errorf("dry_run = false, want true")
	}
	if ship.Message == "" || ship.Branch == "" || ship.Base == "" || ship.Title == "" {
		t.Errorf("dry-run must preview message/branch/base/title; got %+v", ship)
	}
	if !strings.Contains(ship.Branch, "add-a-thing") {
		t.Errorf("previewed branch = %q, want it derived from the message", ship.Branch)
	}
	// Nothing mutated: no commit, still on the default branch, no gh write.
	if after := commitCount(t, repo); after != before {
		t.Errorf("commit count moved %s -> %s under --dry-run", before, after)
	}
	if now := currentBranch(t, repo); now != def {
		t.Errorf("repo left the default branch (%s -> %s) under --dry-run", def, now)
	}
	for _, call := range ghCalls(t, log) {
		if strings.HasPrefix(call, "pr create") || strings.HasPrefix(call, "pr merge") {
			t.Errorf("dry-run invoked a writing gh call: %q", call)
		}
	}
}

// SC-18h — no ship path ever merges.
func TestSC18h_ShipNeverMerges(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	pinDefaultBranch(t, repo)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	run(t, repo, "ship", "-m", "feat(x): add a thing", "--json")

	for _, call := range ghCalls(t, log) {
		if fields := strings.Fields(call); len(fields) >= 2 && fields[0] == "pr" && fields[1] == "merge" {
			t.Fatalf("gh pr merge was invoked: %q", call)
		}
	}
}
