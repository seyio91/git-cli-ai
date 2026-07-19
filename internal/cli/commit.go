package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	appconfig "github.com/seyio91/git-cli-ai/internal/config"
	gitrepo "github.com/seyio91/git-cli-ai/internal/git"
	"github.com/seyio91/git-cli-ai/internal/style"
	"github.com/spf13/cobra"
)

type commitOptions struct {
	message string
	all     bool
}

type commitPayload struct {
	Message string   `json:"message"`
	Files   []string `json:"files"`
	DryRun  bool     `json:"dry_run,omitempty"`
}

func NewCommitCommand(root *Options, out io.Writer) *cobra.Command {
	opts := &commitOptions{}
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Create a formatted git commit",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommit(cmd.Context(), root, opts, out)
		},
	}

	cmd.Flags().StringVarP(&opts.message, "message", "m", "", "commit message")
	cmd.Flags().BoolVar(&opts.all, "all", false, "stage tracked modified and deleted files before committing")

	return cmd
}

func runCommit(ctx context.Context, root *Options, opts *commitOptions, out io.Writer) error {
	resolved, err := appconfig.Load(ctx, "")
	if err != nil {
		return err
	}

	commitStyle := resolved.Config.Commit.Style
	validator, styleErr := style.For(commitStyle)
	if styleErr != nil {
		return fail(styleErr.Error(), "set commit.style to conventional-commits, gitmoji, or freeform-with-rules")
	}

	if opts.message == "" {
		return fail("--message is required", fmt.Sprintf("no message generator is configured yet; supply a message conforming to %s.", commitStyle))
	}

	if result := validator.Validate(opts.message); !result.Valid {
		return fail(fmt.Sprintf("commit message does not conform to %s: %s", commitStyle, result.Reason), "supply a conforming message or configure a provider")
	}

	repo := gitrepo.New("")
	status, err := repo.Status(ctx)
	if err != nil {
		return err
	}

	if opts.all && !root.DryRun {
		if err := repo.AddTracked(ctx); err != nil {
			return err
		}
		status, err = repo.Status(ctx)
		if err != nil {
			return err
		}
	}

	files := status.StagedFiles()
	if root.DryRun && opts.all {
		files = status.CommitCandidateFiles(true)
	}
	if len(files) == 0 {
		return emptyStageError(status)
	}

	if !root.DryRun {
		if err := repo.Commit(ctx, opts.message); err != nil {
			return err
		}
	}

	return writeCommitPayload(out, root.JSON, commitPayload{
		Message: opts.message,
		Files:   files,
		DryRun:  root.DryRun,
	})
}

func emptyStageError(status gitrepo.Status) error {
	changes := status.UnstagedFiles()
	if len(changes) == 0 {
		return fail("nothing staged for commit", "stage files with git add, or use --all to stage tracked modified/deleted files")
	}

	hint := "unstaged changes: " + strings.Join(changes, ", ") + "; stage files with git add, or use --all to stage tracked modified/deleted files"
	if status.HasOnlyUntrackedChanges() {
		hint = "unstaged changes: " + strings.Join(changes, ", ") + "; untracked files are never staged implicitly; stage them explicitly with git add <path>"
	}
	return fail("nothing staged for commit", hint)
}

func writeCommitPayload(out io.Writer, asJSON bool, payload commitPayload) error {
	if asJSON {
		return json.NewEncoder(out).Encode(payload)
	}

	if payload.DryRun {
		_, _ = fmt.Fprintln(out, "dry-run: would commit")
	} else {
		_, _ = fmt.Fprintln(out, "committed")
	}
	_, _ = fmt.Fprintf(out, "message: %s\n", payload.Message)
	_, _ = fmt.Fprintln(out, "files:")
	for _, file := range payload.Files {
		_, _ = fmt.Fprintf(out, "  %s\n", file)
	}
	return nil
}
