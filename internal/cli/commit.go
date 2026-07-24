package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/seyio91/git-cli-ai/internal/ai"
	appconfig "github.com/seyio91/git-cli-ai/internal/config"
	gitrepo "github.com/seyio91/git-cli-ai/internal/git"
	"github.com/seyio91/git-cli-ai/internal/style"
	"github.com/spf13/cobra"
)

type commitOptions struct {
	message     string
	all         bool
	contextOnly bool
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
	cmd.Flags().BoolVar(&opts.contextOnly, "context-only", false, "emit the generation request as JSON without calling a provider or committing")

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

	repo := gitrepo.New("")
	status, err := repo.Status(ctx)
	if err != nil {
		return err
	}

	// --context-only and --dry-run are both inspection modes and neither may
	// touch the index.
	inspecting := root.DryRun || opts.contextOnly

	if opts.all && !inspecting {
		if err := repo.AddTracked(ctx); err != nil {
			return err
		}
		status, err = repo.Status(ctx)
		if err != nil {
			return err
		}
	}

	files := status.StagedFiles()
	if inspecting && opts.all {
		files = status.CommitCandidateFiles(true)
	}
	if len(files) == 0 {
		return emptyStageError(status)
	}

	// Decided before the message is resolved: --context-only hands the request
	// to the caller's own agent and stops, so it must not depend on whether a
	// supplied message happens to conform.
	if opts.contextOnly {
		return writeGenRequest(ctx, out, validator, repo, files, opts.message)
	}

	message := opts.message
	if message == "" || !validator.Validate(message).Valid {
		message, err = resolveMessage(ctx, resolved, validator, commitStyle, repo, files, opts.message, out)
		if err != nil || message == "" {
			return err
		}
	}

	if !root.DryRun {
		if err := repo.Commit(ctx, message); err != nil {
			return err
		}
	}

	return writeCommitPayload(out, root.JSON, commitPayload{
		Message: message,
		Files:   files,
		DryRun:  root.DryRun,
	})
}

// resolveMessage covers the two paths that need a generator: no message at all,
// and a supplied message that does not conform. It returns an empty message with
// a nil error when --context-only has already written the request, which is the
// one case where there is nothing left to commit.
func resolveMessage(
	ctx context.Context,
	resolved appconfig.Resolved,
	validator style.Validator,
	commitStyle string,
	repo gitrepo.Repository,
	files []string,
	intent string,
	out io.Writer,
) (string, error) {
	// The provider is only engaged when a layer actually asked for one. With
	// the built-in default still in force there is nothing configured to call,
	// so the offline behaviour stands.
	configured := resolved.Sources["ai.provider"] != appconfig.LayerDefault
	if !configured {
		if intent == "" {
			return "", fail(
				"--message is required",
				fmt.Sprintf("no ai provider is configured; supply a message conforming to %s, or configure one under [ai.providers]", commitStyle),
			)
		}
		result := validator.Validate(intent)
		return "", fail(
			fmt.Sprintf("commit message does not conform to %s: %s", commitStyle, result.Reason),
			fmt.Sprintf("supply a conforming message, or configure an ai provider under [ai.providers] to render this one into %s", commitStyle),
		)
	}

	diff, err := repo.DiffStaged(ctx)
	if err != nil {
		return "", err
	}

	req := ai.GenRequest{
		Style:   validator.Rules(),
		Diff:    diff,
		Context: contextBlocks(ctx, repo, files),
		Intent:  intent,
	}

	generator, err := ai.New(resolved.Config)
	if err != nil {
		return "", err
	}

	result, err := generator.Generate(ctx, req)
	if err != nil {
		return "", err
	}

	// Exactly one retry, carrying the rejection back so the generator can
	// correct itself. A generator that fails twice is not going to converge.
	if verdict := validator.Validate(result.Message); !verdict.Valid {
		req.Feedback = &ai.Feedback{Message: result.Message, Reason: verdict.Reason}
		result, err = generator.Generate(ctx, req)
		if err != nil {
			return "", err
		}

		if verdict := validator.Validate(result.Message); !verdict.Valid {
			return "", failWithDetails(
				fmt.Sprintf("generated commit message does not conform to %s: %s", commitStyle, verdict.Reason),
				"the provider was re-prompted once with the rejection and still did not conform; supply --message, or point [ai].provider at a more capable model",
				result.Message,
			)
		}
	}

	return result.Message, nil
}

// writeGenRequest emits the provider-neutral request for the caller's own agent
// to act on. No provider is resolved, so it works with none configured.
func writeGenRequest(
	ctx context.Context,
	out io.Writer,
	validator style.Validator,
	repo gitrepo.Repository,
	files []string,
	intent string,
) error {
	diff, err := repo.DiffStaged(ctx)
	if err != nil {
		return err
	}

	return json.NewEncoder(out).Encode(ai.GenRequest{
		Style:   validator.Rules(),
		Diff:    diff,
		Context: contextBlocks(ctx, repo, files),
		Intent:  intent,
	})
}

func contextBlocks(ctx context.Context, repo gitrepo.Repository, files []string) []ai.ContextBlock {
	blocks := []ai.ContextBlock{{Label: "staged files", Content: strings.Join(files, "\n")}}

	// A detached HEAD is a legitimate state to commit from, so a missing branch
	// name drops the block rather than failing the commit.
	if branch, err := repo.CurrentBranch(ctx); err == nil {
		blocks = append(blocks, ai.ContextBlock{Label: "branch", Content: branch})
	}
	return withMemory(blocks, memoryContext(ctx, repo))
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
