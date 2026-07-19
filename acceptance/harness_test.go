// Package acceptance drives the compiled git-cli binary against throwaway git
// repositories. These tests are the executable form of the plan's success
// criteria: each test name carries the SC-NN id of the criterion it covers.
package acceptance

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	// The binary is built at runtime by TestMain, which is invisible to the
	// test cache: without this import the code under test is not in this
	// package's dependency graph, so `go test ./...` replays a cached PASS
	// after the implementation changes. Blank-importing the root command pulls
	// in cli, config, style, git and conventional, so a change to any of them
	// invalidates the cache. Verified by mutation: with this import removed, a
	// deliberately broken commit path still reported `ok (cached)`.
	_ "github.com/seyio91/git-cli-ai/internal/cli"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "git-cli-acceptance")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	binary = filepath.Join(dir, "git-cli")
	build := exec.Command("go", "build", "-o", binary, "../cmd/git-cli")
	if out, err := build.CombinedOutput(); err != nil {
		panic("building git-cli: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

type result struct {
	stdout   string
	stderr   string
	exitCode int
}

// payload decodes stdout as the tool's JSON contract. Both the success and the
// error shapes are decoded into one struct so a test can assert on whichever
// fields apply.
func (r result) payload(t *testing.T) struct {
	Message     string   `json:"message"`
	Files       []string `json:"files"`
	DryRun      bool     `json:"dry_run"`
	Error       string   `json:"error"`
	Hint        string   `json:"hint"`
	Details     string   `json:"details"`
	GitExitCode int      `json:"git_exit_code"`
} {
	t.Helper()
	var p struct {
		Message     string   `json:"message"`
		Files       []string `json:"files"`
		DryRun      bool     `json:"dry_run"`
		Error       string   `json:"error"`
		Hint        string   `json:"hint"`
		Details     string   `json:"details"`
		GitExitCode int      `json:"git_exit_code"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &p); err != nil {
		t.Fatalf("stdout is not valid JSON (%v): %q", err, r.stdout)
	}
	return p
}

// hermeticEnv isolates git from the developer's global and system config so
// results do not depend on the machine running the tests. It also puts a
// test-controlled directory first on PATH, which is how `gh` is intercepted —
// see writeFakeGH.
func hermeticEnv(home string) []string {
	return append(os.Environ(),
		"HOME="+home,
		"PATH="+ghBinDir(home)+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Pin the XDG root inside the throwaway repo so the tool's global
		// config never resolves to the developer's real ~/.config.
		"XDG_CONFIG_HOME="+filepath.Join(home, ".xdg"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
}

// newRepo creates an initialised git repository with one commit, so that HEAD
// exists and rename detection has a baseline to compare against.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	mustGit(t, dir, "init", "-q", ".")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "test")

	writeFile(t, dir, "seed.txt", "seed\n")
	mustGit(t, dir, "add", "seed.txt")
	mustGit(t, dir, "commit", "-qm", "chore: seed")

	// Fail closed: any `gh` call from a test that has not installed a fake
	// exits nonzero rather than reaching GitHub. A forgotten fake is then a
	// loud test failure instead of a silent network call.
	installGH(t, dir, `#!/bin/sh
echo "real gh must never be called from the acceptance suite; install a fake with writeFakeGH" >&2
exit 97
`)

	return dir
}

// ghBinDir is inside .git deliberately: anywhere else in the working tree and
// the fake would show up as an untracked file, which several criteria assert
// on. git never reports paths under .git.
func ghBinDir(repo string) string {
	return filepath.Join(repo, ".git", "bin")
}

// installGH writes an executable named gh into the directory hermeticEnv puts
// first on PATH.
func installGH(t *testing.T, repo string, script string) {
	t.Helper()

	path := filepath.Join(ghBinDir(repo), "gh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeFakeGH replaces the poison pill with a fake that appends its argv to a
// log and prints body. The log is what lets a test assert on which subcommands
// ran — including that `merge` never did.
func writeFakeGH(t *testing.T, repo string, body string) (log string) {
	t.Helper()

	// Also inside .git, for the same reason as the fake itself: a log file in
	// the working tree would be an untracked path the tool can see.
	log = filepath.Join(ghBinDir(repo), "gh.log")
	installGH(t, repo, "#!/bin/sh\necho \"$@\" >> "+log+"\n"+body+"\n")
	return log
}

// runGH invokes gh resolved through the hermetic PATH, so a test can prove the
// interception works.
//
// t.Setenv is load-bearing rather than incidental: exec.Command resolves a
// bare binary name with LookPath against the *calling* process's PATH and
// ignores cmd.Env entirely. Setting cmd.Env alone finds the developer's real
// gh. The tool under test is unaffected — it is a child process whose own
// environment is the cmd.Env we pass, so its lookup sees the fake — but a
// helper running in-process has to change PATH for real.
func runGH(t *testing.T, repo string, args ...string) (string, error) {
	t.Helper()

	t.Setenv("PATH", ghBinDir(repo)+string(os.PathListSeparator)+os.Getenv("PATH"))

	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	cmd.Env = hermeticEnv(repo)

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ghCalls returns the recorded argv lines, one per invocation.
func ghCalls(t *testing.T, log string) []string {
	t.Helper()

	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	out := strings.TrimSpace(string(data))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func run(t *testing.T, repo string, args ...string) result {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Dir = repo
	cmd.Env = hermeticEnv(repo)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}

	return result{stdout: stdout.String(), stderr: stderr.String(), exitCode: code}
}

// runWithoutXDG drops XDG_CONFIG_HOME so the global-config lookup exercises its
// ~/.config fallback branch.
func runWithoutXDG(t *testing.T, repo string, args ...string) result {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Dir = repo

	env := make([]string, 0, len(hermeticEnv(repo)))
	for _, entry := range hermeticEnv(repo) {
		if strings.HasPrefix(entry, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = env

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}

	return result{stdout: stdout.String(), stderr: stderr.String(), exitCode: code}
}

func mustGit(t *testing.T, repo string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Env = hermeticEnv(repo)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, repo string, name string, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFileAt(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// configPayload is the shape `git-cli config --json` emits: the resolved
// configuration plus, for each dotted key, the layer that supplied its value.
type configPayload struct {
	Config  map[string]any    `json:"config"`
	Sources map[string]string `json:"sources"`
}

func decodeConfig(t *testing.T, r result) configPayload {
	t.Helper()
	var p configPayload
	if err := json.Unmarshal([]byte(r.stdout), &p); err != nil {
		t.Fatalf("stdout is not a config payload (%v): %q", err, r.stdout)
	}
	return p
}

// configValue walks a dotted path through the resolved config tree.
func configValue(t *testing.T, r result, path string) string {
	t.Helper()

	var current any = decodeConfig(t, r).Config
	for _, segment := range strings.Split(path, ".") {
		block, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("config path %q: %q is not a table", path, segment)
		}
		current, ok = block[segment]
		if !ok {
			t.Fatalf("config path %q not present in %q", path, r.stdout)
		}
	}

	value, ok := current.(string)
	if !ok {
		t.Fatalf("config path %q is not a string: %#v", path, current)
	}
	return value
}

func configSource(t *testing.T, r result, path string) string {
	t.Helper()

	sources := decodeConfig(t, r).Sources
	source, ok := sources[path]
	if !ok {
		t.Fatalf("no provenance recorded for %q; have %#v", path, sources)
	}
	return source
}

func removeFile(t *testing.T, repo string, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(repo, name)); err != nil {
		t.Fatal(err)
	}
}

func commitCount(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(mustGit(t, repo, "rev-list", "--count", "HEAD"))
}

// commitMessage returns the raw stored message from the commit object, which is
// the only authoritative source: `git log --format=%B` appends its own trailing
// newline and would mask a round-trip difference.
func commitMessage(t *testing.T, repo string) string {
	t.Helper()

	raw := mustGit(t, repo, "cat-file", "commit", "HEAD")
	_, message, ok := strings.Cut(raw, "\n\n")
	if !ok {
		t.Fatalf("could not split commit object header from message: %q", raw)
	}
	return message
}

func untrackedFiles(t *testing.T, repo string) []string {
	t.Helper()
	out := strings.TrimSpace(mustGit(t, repo, "ls-files", "--others", "--exclude-standard"))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func committedFiles(t *testing.T, repo string) []string {
	t.Helper()
	out := strings.TrimSpace(mustGit(t, repo, "show", "--stat", "--format=", "--name-only", "HEAD"))
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}
