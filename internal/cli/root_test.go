package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/seyio91/git-cli-ai/internal/git"
)

func TestWantsJSONScansRawArgs(t *testing.T) {
	tests := map[string]struct {
		args []string
		want bool
	}{
		"absent":                  {[]string{"commit", "-m", "feat: x"}, false},
		"bare flag":               {[]string{"commit", "--json"}, true},
		"before subcommand":       {[]string{"--json", "commit"}, true},
		"explicit true":           {[]string{"commit", "--json=true"}, true},
		"explicit false":          {[]string{"commit", "--json=false"}, false},
		"last occurrence wins":    {[]string{"--json", "commit", "--json=false"}, false},
		"ignored after separator": {[]string{"commit", "--", "--json"}, false},
		"alongside a bad flag":    {[]string{"commit", "--bogus", "--json"}, true},
		"not a prefix match":      {[]string{"commit", "--jsonish"}, false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := wantsJSON(tt.args); got != tt.want {
				t.Fatalf("wantsJSON(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDescribeErrorKeepsStderrOutOfTheHint(t *testing.T) {
	stderr := "fatal: not a git repository (or any of the parent directories): .git"
	ge := &git.CommandError{
		Args:     []string{"status", "--porcelain=v1", "-z"},
		ExitCode: 128,
		Stderr:   stderr,
	}

	f := describeError(ge)

	if f.hint == stderr {
		t.Fatal("hint is a bare stderr dump; it must be actionable guidance")
	}
	if !strings.Contains(f.hint, "git init") {
		t.Fatalf("hint = %q, want actionable guidance", f.hint)
	}
	if f.details != stderr {
		t.Fatalf("details = %q, want the raw stderr preserved", f.details)
	}
	if f.gitExitCode != 128 {
		t.Fatalf("gitExitCode = %d, want 128", f.gitExitCode)
	}
}

func TestDescribeErrorHandlesDetachedHead(t *testing.T) {
	f := describeError(git.ErrDetachedHead)

	if !strings.Contains(f.hint, "checkout") {
		t.Fatalf("hint = %q, want guidance on getting onto a branch", f.hint)
	}
	if f.message == "" {
		t.Fatal("expected a message")
	}
}

func TestGitHintMapsCommonFailures(t *testing.T) {
	tests := map[string]struct {
		stderr   string
		exitCode int
		want     string
	}{
		"not a repo":       {"fatal: not a git repository", 128, "git init"},
		"index lock":       {"fatal: Unable to create '/r/.git/index.lock': Operation not permitted", 128, "index.lock"},
		"permission":       {"error: insufficient permission for adding: Permission denied", 128, "permissions"},
		"missing identity": {"fatal: unable to auto-detect email address\n*** Please tell me who you are.", 128, "user.email"},
		"no upstream":      {"fatal: no upstream configured for branch 'master'", 128, "push -u"},
		"binary missing":   {"", -1, "PATH"},
		"unrecognised":     {"fatal: something entirely new", 1, "manually"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := gitHint(&git.CommandError{Stderr: tt.stderr, ExitCode: tt.exitCode})
			if !strings.Contains(got, tt.want) {
				t.Fatalf("gitHint = %q, want it to mention %q", got, tt.want)
			}
			if got == tt.stderr && tt.stderr != "" {
				t.Fatal("hint must not echo stderr verbatim")
			}
		})
	}
}

func TestCommandErrorSurfacesExitCodeWhenStderrEmpty(t *testing.T) {
	ge := &git.CommandError{Args: []string{"status"}, ExitCode: 128}

	if got := ge.Error(); !strings.Contains(got, "128") {
		t.Fatalf("Error() = %q, want it to surface the exit code", got)
	}
}

func TestCommandErrorUnwraps(t *testing.T) {
	inner := errors.New("boom")
	ge := &git.CommandError{Args: []string{"status"}, Err: inner}

	if !errors.Is(ge, inner) {
		t.Fatal("expected CommandError to unwrap to its cause")
	}
}
