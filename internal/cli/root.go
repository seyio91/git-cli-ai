package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/seyio91/git-cli-ai/internal/git"
	"github.com/spf13/cobra"
)

type Options struct {
	JSON   bool
	DryRun bool
}

type appError struct {
	message string
	hint    string
}

type errorPayload struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}

func (e appError) Error() string {
	return e.message
}

func Execute() {
	opts := &Options{}
	cmd := NewRootCommand(opts, os.Stdout, os.Stderr)

	if err := cmd.Execute(); err != nil {
		writeError(opts, os.Stdout, os.Stderr, err)
		os.Exit(1)
	}
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

	return cmd
}

func fail(message string, hint string) error {
	return appError{message: message, hint: hint}
}

func writeError(opts *Options, out io.Writer, errOut io.Writer, err error) {
	message, hint := describeError(err)
	if opts.JSON {
		_ = json.NewEncoder(out).Encode(errorPayload{Error: message, Hint: hint})
		return
	}

	_, _ = fmt.Fprintf(errOut, "Error: %s\n", message)
	if hint != "" {
		_, _ = fmt.Fprintf(errOut, "Hint: %s\n", hint)
	}
}

func describeError(err error) (string, string) {
	var ae appError
	if errors.As(err, &ae) {
		return ae.message, ae.hint
	}

	var ge *git.CommandError
	if errors.As(err, &ge) {
		hint := ge.Stderr
		if hint == "" {
			hint = "ensure git is installed and the current directory is a git repository"
		}
		return fmt.Sprintf("git command failed: git %s", ge.ArgString()), hint
	}

	return err.Error(), ""
}
