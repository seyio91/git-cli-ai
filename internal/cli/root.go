package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/seyio91/git-cli-ai/internal/ai"
	appconfig "github.com/seyio91/git-cli-ai/internal/config"
	"github.com/seyio91/git-cli-ai/internal/git"
	"github.com/seyio91/git-cli-ai/internal/pr"
	"github.com/spf13/cobra"
)

type Options struct {
	JSON   bool
	DryRun bool
}

type appError struct {
	message string
	hint    string
	details string
}

// errorPayload is the agent-facing error contract. hint is always actionable
// guidance; git's own words go in details so the hint is never a bare stderr
// dump.
type errorPayload struct {
	Error       string `json:"error"`
	Hint        string `json:"hint,omitempty"`
	Details     string `json:"details,omitempty"`
	GitExitCode int    `json:"git_exit_code,omitempty"`
}

type failure struct {
	message     string
	hint        string
	details     string
	gitExitCode int
}

func (e appError) Error() string {
	return e.message
}

func Execute() {
	opts := &Options{}
	cmd := NewRootCommand(opts, os.Stdout, os.Stderr)

	// Seed from a raw scan of argv. Registering the flag resets opts.JSON to
	// its default, and a flag-parse failure aborts before cobra ever populates
	// it — so without this, malformed input degrades to text output and an
	// agent parsing stdout as JSON sees nothing. A successful parse overwrites
	// this with the real value.
	opts.JSON = wantsJSON(os.Args[1:])

	if err := cmd.Execute(); err != nil {
		writeError(opts, os.Stdout, os.Stderr, err)
		os.Exit(1)
	}
}

func wantsJSON(args []string) bool {
	found := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		switch arg {
		case "--json", "--json=true":
			found = true
		case "--json=false":
			found = false
		}
	}
	return found
}

func NewRootCommand(opts *Options, out io.Writer, errOut io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "git-cli",
		Short:         "Git workflow helper",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().BoolVar(&opts.JSON, "json", false, "emit JSON output")
	cmd.PersistentFlags().BoolVar(&opts.DryRun, "dry-run", false, "preview without mutating")
	cmd.AddCommand(NewCommitCommand(opts, out))
	cmd.AddCommand(NewConfigCommand(opts, out))
	cmd.AddCommand(NewPRCommand(opts, out))

	return cmd
}

func fail(message string, hint string) error {
	return appError{message: message, hint: hint}
}

// failWithDetails is fail for the cases that must show the offending value —
// the hint stays actionable guidance, the value goes in details.
func failWithDetails(message string, hint string, details string) error {
	return appError{message: message, hint: hint, details: details}
}

func writeError(opts *Options, out io.Writer, errOut io.Writer, err error) {
	f := describeError(err)
	if opts.JSON {
		_ = json.NewEncoder(out).Encode(errorPayload{
			Error:       f.message,
			Hint:        f.hint,
			Details:     f.details,
			GitExitCode: f.gitExitCode,
		})
		return
	}

	_, _ = fmt.Fprintf(errOut, "Error: %s\n", f.message)
	if f.hint != "" {
		_, _ = fmt.Fprintf(errOut, "Hint: %s\n", f.hint)
	}
	if f.details != "" {
		_, _ = fmt.Fprintf(errOut, "Details: %s\n", f.details)
	}
}

func describeError(err error) failure {
	var ae appError
	if errors.As(err, &ae) {
		return failure{message: ae.message, hint: ae.hint, details: ae.details}
	}

	var ce *appconfig.LoadError
	if errors.As(err, &ce) {
		return failure{message: ce.Message, hint: ce.Hint, details: ce.Details}
	}

	var pe *ai.ProviderError
	if errors.As(err, &pe) {
		return failure{message: pe.Message, hint: pe.Hint, details: pe.Details}
	}

	var fe *pr.Error
	if errors.As(err, &fe) {
		return failure{message: fe.Message, hint: fe.Hint, details: fe.Details}
	}

	if errors.Is(err, git.ErrDetachedHead) {
		return failure{
			message: "HEAD is not on a named branch",
			hint:    "check out a branch with `git checkout -b <name>` before committing",
		}
	}

	var ge *git.CommandError
	if errors.As(err, &ge) {
		return failure{
			message:     fmt.Sprintf("git command failed: git %s", ge.ArgString()),
			hint:        gitHint(ge),
			details:     ge.Stderr,
			gitExitCode: ge.ExitCode,
		}
	}

	return failure{message: err.Error()}
}

// gitHint maps a git failure to actionable guidance. The plan requires that the
// agent path never receive a bare stderr dump as its hint; the raw text is
// still available in the details field.
func gitHint(ge *git.CommandError) string {
	stderr := strings.ToLower(ge.Stderr)

	switch {
	case ge.ExitCode < 0:
		return "git could not be executed; ensure it is installed and on PATH"
	case strings.Contains(stderr, "not a git repository"):
		return "run this inside a git repository, or initialise one with `git init`"
	case strings.Contains(stderr, "index.lock"):
		return "another git process may be holding .git/index.lock, or the repository is not writable; remove the lock if it is stale"
	case strings.Contains(stderr, "permission denied"), strings.Contains(stderr, "operation not permitted"):
		return "git could not write to the repository; check the file permissions on .git"
	case strings.Contains(stderr, "please tell me who you are"), strings.Contains(stderr, "empty ident"):
		return "set a commit identity with `git config user.name` and `git config user.email`"
	case strings.Contains(stderr, "non-fast-forward"), strings.Contains(stderr, "rejected"):
		return "the remote has commits this branch does not; pull or rebase onto the upstream, then retry"
	case strings.Contains(stderr, "could not read from remote"), strings.Contains(stderr, "authentication failed"),
		strings.Contains(stderr, "permission denied (publickey)"):
		return "the remote rejected the connection; check the remote URL and that your credentials or SSH key are available to this shell"
	case strings.Contains(stderr, "no upstream"), strings.Contains(stderr, "no such remote"):
		return "set an upstream with `git push -u <remote> <branch>`"
	}

	return "run the same git command manually to see its full output"
}
