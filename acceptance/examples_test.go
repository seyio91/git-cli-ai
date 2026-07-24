package acceptance

import (
	"os"
	"path/filepath"
	"testing"
)

// SC-32 — the shipped example config files must be valid input to the config
// loader. They are placed at the real global and repo locations and resolved
// through the compiled binary, so an unknown or malformed key — which the
// loader rejects with DisallowUnknownFields — fails this test rather than
// silently shipping a broken example. Asserted by loading, not by eye.

func readExample(t *testing.T, name string) string {
	t.Helper()
	// The test binary runs in the acceptance package dir; examples/ is one up.
	data, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatalf("cannot read example %s: %v", name, err)
	}
	return string(data)
}

func TestSC32_GlobalExampleLoads(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, readExample(t, "config.global.toml"))

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("global example did not load: exit %d, stdout %q, stderr %q", res.exitCode, res.stdout, res.stderr)
	}
	// A value from the example resolves and is attributed to the global layer.
	if got := configValue(t, res, "ai.commit_model"); got != "gpt-4.1-mini" {
		t.Errorf("ai.commit_model = %q, want the example's gpt-4.1-mini", got)
	}
	if src := configSource(t, res, "ai.commit_model"); src != "global" {
		t.Errorf("ai.commit_model source = %q, want global", src)
	}
}

func TestSC32_RepoExampleLoads(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, readExample(t, "config.repo.toml"))

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("repo example did not load: exit %d, stdout %q, stderr %q", res.exitCode, res.stdout, res.stderr)
	}
	if got := configValue(t, res, "branch.pattern"); got != "{slug}" {
		t.Errorf("branch.pattern = %q, want the example's {slug}", got)
	}
	if src := configSource(t, res, "branch.pattern"); src != "repo" {
		t.Errorf("branch.pattern source = %q, want repo", src)
	}
}

// Both layers at once: the repo example must override the global example per
// key, which is the whole point of shipping the pair.
func TestSC32_ExamplesComposeAcrossLayers(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, readExample(t, "config.global.toml"))
	writeRepoConfig(t, repo, readExample(t, "config.repo.toml"))

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("examples did not load together: exit %d, stderr %q", res.exitCode, res.stderr)
	}
	// branch.pattern comes from the repo file; ai.pr_model is only in the
	// global file and survives.
	if got := configValue(t, res, "branch.pattern"); got != "{slug}" {
		t.Errorf("branch.pattern = %q, want repo's {slug}", got)
	}
	if got := configValue(t, res, "ai.pr_model"); got != "gpt-4.1" {
		t.Errorf("ai.pr_model = %q, want global's gpt-4.1", got)
	}
}
