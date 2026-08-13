package acceptance

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

var hexCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type versionOut struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dirty   bool   `json:"dirty"`
}

// The default TestMain binary is built with no ldflags, so it reports "dev".
func TestVersion_DefaultBuildReportsDev(t *testing.T) {
	repo := newRepo(t)
	var v versionOut
	if err := json.Unmarshal([]byte(run(t, repo, "version", "--json").stdout), &v); err != nil {
		t.Fatalf("version --json not JSON: %v", err)
	}
	if v.Version != "dev" {
		t.Errorf("version = %q, want dev for an unstamped build", v.Version)
	}
}

// A release build stamps the version via ldflags, and the commit is embedded
// from the repo either way — this is the "releases + commit versions" contract.
func TestVersion_LdflagsStampAndEmbeddedCommit(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "git-cli")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=v9.9.9-acc", "-o", bin, "../cmd/git-cli")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	repo := newRepo(t)
	cmd := exec.Command(bin, "version", "--json")
	cmd.Dir = repo
	cmd.Env = hermeticEnv(repo)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}

	var v versionOut
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("not JSON (%v): %s", err, out)
	}
	if v.Version != "v9.9.9-acc" {
		t.Errorf("version = %q, want the ldflags stamp v9.9.9-acc", v.Version)
	}
	if !hexCommit.MatchString(v.Commit) {
		t.Errorf("commit = %q, want the 40-char SHA embedded from the repo", v.Commit)
	}
}
