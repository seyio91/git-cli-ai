package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

// Phase 2 criteria. These are written before the implementation exists and are
// expected to fail until the config system lands.

// writeGlobalConfig places a config file at the XDG location. newRepo sets HOME
// and the harness exports XDG_CONFIG_HOME under it, so this stays hermetic.
func writeGlobalConfig(t *testing.T, repo string, content string) {
	t.Helper()
	writeFileAt(t, filepath.Join(repo, ".xdg", "git-cli", "config.toml"), content)
}

func writeRepoConfig(t *testing.T, repo string, content string) {
	t.Helper()
	writeFileAt(t, filepath.Join(repo, ".git-cli.toml"), content)
}

// SC-10 — repo overrides global, global overrides built-in defaults.
func TestSC10_PrecedenceRepoOverGlobalOverDefaults(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "commit.style"); got != "gitmoji" {
		t.Fatalf("commit.style = %q, want the global value", got)
	}

	writeRepoConfig(t, repo, "[commit]\nstyle = \"conventional-commits\"\n")

	res = run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "commit.style"); got != "conventional-commits" {
		t.Fatalf("commit.style = %q, want the repo value to win", got)
	}
}

// SC-10 — merging is per key: a repo file that sets one key inside a block must
// not discard the other keys the global file set in that same block.
func TestSC10_MergeIsPerKeyNotPerBlock(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, "[ai]\nprovider = \"anthropic\"\ncommit_model = \"claude-haiku-4-5\"\n")
	writeRepoConfig(t, repo, "[ai]\ncommit_model = \"claude-sonnet-5\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "ai.commit_model"); got != "claude-sonnet-5" {
		t.Fatalf("ai.commit_model = %q, want the repo override", got)
	}
	if got := configValue(t, res, "ai.provider"); got != "anthropic" {
		t.Fatalf("ai.provider = %q, want the global value to survive a partial repo override", got)
	}
}

// SC-10 — the repo file is found from the repository root, so results do not
// depend on which subdirectory the tool runs in.
func TestSC10_RepoConfigFoundFromSubdirectory(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")
	writeFile(t, repo, "nested/deep/keep.txt", "x\n")

	res := run(t, filepath.Join(repo, "nested", "deep"), "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "commit.style"); got != "gitmoji" {
		t.Fatalf("commit.style = %q, want the repo-root config to apply from a subdirectory", got)
	}
}

// SC-10 — an explicitly-set zero value in the repo layer must override a
// non-empty global value. This is the classic layered-config trap: if optional
// fields are plain strings rather than pointers, `model = ""` is
// indistinguishable from "key absent" and the global value wrongly survives.
func TestSC10_ExplicitZeroValueOverridesGlobal(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, "[ai]\nmodel = \"global-model\"\n")
	writeRepoConfig(t, repo, "[ai]\nmodel = \"\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "ai.model"); got != "" {
		t.Fatalf("ai.model = %q, want the explicit empty override to win", got)
	}
	if got := configSource(t, res, "ai.model"); got != "repo" {
		t.Fatalf("ai.model source = %q, want \"repo\"", got)
	}
}

// SC-10 — an empty repo file must not clobber values the global layer set.
func TestSC10_EmptyRepoFileDoesNotClobberGlobal(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, "[ai]\nmodel = \"global-model\"\n")
	writeRepoConfig(t, repo, "")

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "ai.model"); got != "global-model" {
		t.Fatalf("ai.model = %q, want the global value to survive", got)
	}
}

// SC-10 — with XDG_CONFIG_HOME unset, the global config resolves under
// ~/.config. The rest of the suite always sets XDG_CONFIG_HOME, so without this
// the fallback branch would ship unexercised.
func TestSC10_GlobalConfigFallsBackToHomeConfig(t *testing.T) {
	repo := newRepo(t)
	writeFileAt(t, filepath.Join(repo, ".config", "git-cli", "config.toml"), "[commit]\nstyle = \"gitmoji\"\n")

	res := runWithoutXDG(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "commit.style"); got != "gitmoji" {
		t.Fatalf("commit.style = %q, want the ~/.config fallback to apply", got)
	}
	if got := configSource(t, res, "commit.style"); got != "global" {
		t.Fatalf("commit.style source = %q, want \"global\"", got)
	}
}

// SC-12 — a layer that sets a value identical to the built-in default is still
// attributed to that layer, not to "default".
func TestSC12_ProvenanceReportsLayerEvenWhenValueEqualsDefault(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstyle = \"conventional-commits\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configSource(t, res, "commit.style"); got != "repo" {
		t.Fatalf("commit.style source = %q, want \"repo\" even though the value matches the default", got)
	}
}

// SC-13 — outside a git repository there is no repo layer, but global and
// defaults are still meaningful, so `config` must degrade rather than fail.
func TestSC13_ConfigWorksOutsideAGitRepository(t *testing.T) {
	dir := t.TempDir()
	writeFileAt(t, filepath.Join(dir, ".xdg", "git-cli", "config.toml"), "[commit]\nstyle = \"gitmoji\"\n")

	res := run(t, dir, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 outside a git repo (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "commit.style"); got != "gitmoji" {
		t.Fatalf("commit.style = %q, want the global layer to still apply", got)
	}
}

// SC-11 — an unknown key is a loud failure naming the key and the file.
func TestSC11_UnknownKeyErrorsNamingKeyAndFile(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstlye = \"gitmoji\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for an unknown config key")
	}

	p := res.payload(t)
	combined := p.Error + " " + p.Hint + " " + p.Details
	if !strings.Contains(combined, "stlye") {
		t.Fatalf("error did not name the offending key: %q", combined)
	}
	if !strings.Contains(combined, ".git-cli.toml") {
		t.Fatalf("error did not name the offending file: %q", combined)
	}
}

// SC-11 — malformed TOML fails with a structured error rather than a panic.
func TestSC11_MalformedTOMLErrorsCleanly(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit\nstyle = ")

	res := run(t, repo, "config", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for malformed TOML")
	}
	if p := res.payload(t); p.Error == "" {
		t.Fatalf("expected a structured error, got %q", res.stdout)
	}
}

// SC-11 — a value outside the permitted set is rejected.
func TestSC11_InvalidValueIsRejected(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstyle = \"haiku-only\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode == 0 {
		t.Fatal("expected nonzero exit for an invalid commit style")
	}
	if p := res.payload(t); !strings.Contains(p.Error+p.Hint, "haiku-only") {
		t.Fatalf("error should name the rejected value: %q / %q", p.Error, p.Hint)
	}
}

// SC-12 — config reports which layer supplied each value.
func TestSC12_ConfigReportsPerKeyProvenance(t *testing.T) {
	repo := newRepo(t)
	writeGlobalConfig(t, repo, "[ai]\nprovider = \"anthropic\"\n")
	writeRepoConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}

	if got := configSource(t, res, "commit.style"); got != "repo" {
		t.Fatalf("commit.style source = %q, want \"repo\"", got)
	}
	if got := configSource(t, res, "ai.provider"); got != "global" {
		t.Fatalf("ai.provider source = %q, want \"global\"", got)
	}
}

// SC-12 — without --json the output is human-readable and still shows values.
func TestSC12_ConfigHumanOutputShowsResolvedValues(t *testing.T) {
	repo := newRepo(t)
	writeRepoConfig(t, repo, "[commit]\nstyle = \"gitmoji\"\n")

	res := run(t, repo, "config")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
	if !strings.Contains(res.stdout, "gitmoji") {
		t.Fatalf("stdout = %q, want the resolved value", res.stdout)
	}
}

// SC-13 — with no config files at all, defaults apply and the command succeeds.
func TestSC13_AbsentConfigFilesUseDefaults(t *testing.T) {
	repo := newRepo(t)

	res := run(t, repo, "config", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 with no config present (stderr: %s)", res.exitCode, res.stderr)
	}
	if got := configValue(t, res, "commit.style"); got != "conventional-commits" {
		t.Fatalf("commit.style = %q, want the built-in default", got)
	}
	if got := configSource(t, res, "commit.style"); got != "default" {
		t.Fatalf("commit.style source = %q, want \"default\"", got)
	}
}

// SC-13 — the Phase 1 commit path keeps working with no config present.
func TestSC13_CommitStillWorksWithoutConfig(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "a.txt", "a\n")
	mustGit(t, repo, "add", "a.txt")

	res := run(t, repo, "commit", "--message", "feat(x): thing", "--json")
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exitCode, res.stderr)
	}
}
