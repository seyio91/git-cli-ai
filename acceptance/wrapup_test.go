package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v1 wrap-up: --dry-run must make no provider call (W1), and an explicit --base
// that disagrees with an existing open PR must error (W2).

// sentinelProvider configures a cli provider whose command touches a sentinel
// file when invoked and echoes a body. Its presence after a run proves the
// provider was called; its absence proves it was not.
func sentinelProvider(t *testing.T, repo string) (sentinel string) {
	t.Helper()
	sentinel = filepath.Join(repo, ".git", "provider-called")
	cfg := "[ai]\nprovider = \"sentinel\"\n\n" +
		"[ai.providers.sentinel]\ntype = \"cli\"\n" +
		"command = [\"sh\", \"-c\", \"touch " + sentinel + "; echo generated body\"]\n"
	writeFile(t, repo, ".git-cli.toml", cfg)
	mustGit(t, repo, "add", ".git-cli.toml")
	mustGit(t, repo, "commit", "-qm", "chore: sentinel provider")
	return sentinel
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// W1 — pr --dry-run does not call the provider (no billable generation for a
// preview). The control run proves the setup would call it.
func TestWrapup_DryRunMakesNoProviderCall(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	mustGit(t, repo, "checkout", "-q", "-b", "feat/thing")
	sentinel := sentinelProvider(t, repo)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")
	mustGit(t, repo, "commit", "-qm", "feat: a thing")
	writeFakeGH(t, repo, ghCreateSucceeds)

	if r := run(t, repo, "pr", "--dry-run"); r.exitCode != 0 {
		t.Fatalf("dry-run exit = %d, stderr %q", r.exitCode, r.stderr)
	}
	if exists(sentinel) {
		t.Errorf("provider was invoked under --dry-run")
	}

	// Control: a real run must call it, or the test above proves nothing.
	if r := run(t, repo, "pr"); r.exitCode != 0 {
		t.Fatalf("real run exit = %d, stderr %q", r.exitCode, r.stderr)
	}
	if !exists(sentinel) {
		t.Errorf("provider was not invoked on a real run — the dry-run assertion is vacuous")
	}
}

// ghExistingPRBase answers pr list with an open PR on a chosen base.
func ghExistingPRBase(base string) string {
	return `case "$1 $2" in
  "pr list") echo '[{"url":"` + prURL + `","baseRefName":"` + base + `"}]' ;;
  "repo view") echo '{"defaultBranchRef":{"name":"main"}}' ;;
  "pr create") echo "MUST NOT CREATE" >&2; exit 66 ;;
esac`
}

// W2 — an explicit --base that disagrees with the existing PR's base errors.
func TestWrapup_BaseMismatchErrors(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	writeFakeGH(t, repo, ghExistingPRBase("main"))

	res := run(t, repo, "pr", "--base", "develop", "--json")
	if res.exitCode == 0 {
		t.Fatalf("exit = 0, want an error (stdout %q)", res.stdout)
	}
	p := res.payload(t)
	if !strings.Contains(p.Error, "main") || !strings.Contains(p.Error, "develop") {
		t.Errorf("error = %q, want it to name both bases", p.Error)
	}
}

// W2 — a matching --base still converges on the existing PR.
func TestWrapup_BaseMatchConverges(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	writeFakeGH(t, repo, ghExistingPRBase("main"))

	res := run(t, repo, "pr", "--base", "main", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want convergence on the existing PR (stderr %q)", res.exitCode, res.stderr)
	}
	ship := decodeShip(t, res)
	if !ship.Existing || ship.URL != prURL {
		t.Errorf("want existing PR URL, got %+v", ship)
	}
}

// W2 — with no --base at all there is no disagreement to report.
func TestWrapup_NoBaseConverges(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	writeFakeGH(t, repo, ghExistingPRBase("develop"))

	res := run(t, repo, "pr", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want convergence (stderr %q)", res.exitCode, res.stderr)
	}
}
