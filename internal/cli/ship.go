package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	branchpkg "github.com/seyio91/git-cli-ai/internal/branch"
	appconfig "github.com/seyio91/git-cli-ai/internal/config"
	gitrepo "github.com/seyio91/git-cli-ai/internal/git"
	"github.com/seyio91/git-cli-ai/internal/pr"
	"github.com/seyio91/git-cli-ai/internal/style"
	"github.com/spf13/cobra"
)

type shipOptions struct {
	message  string
	all      bool
	title    string
	body     string
	bodyFile string
	base     string
}

type shipPayload struct {
	Message       string `json:"message,omitempty"`
	Branch        string `json:"branch"`
	Base          string `json:"base"`
	Title         string `json:"title,omitempty"`
	URL           string `json:"url,omitempty"`
	CreatedBranch bool   `json:"created_branch,omitempty"`
	Existing      bool   `json:"existing,omitempty"`
	Pushed        bool   `json:"pushed,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
}

func NewShipCommand(root *Options, out io.Writer) *cobra.Command {
	opts := &shipOptions{}
	cmd := &cobra.Command{
		Use:   "ship",
		Short: "Stage, commit, push and open a pull request in one step",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShip(cmd.Context(), root, opts, out)
		},
	}

	cmd.Flags().StringVarP(&opts.message, "message", "m", "", "commit message")
	cmd.Flags().BoolVar(&opts.all, "all", false, "stage tracked modified and deleted files before committing")
	cmd.Flags().StringVar(&opts.title, "title", "", "pull request title")
	cmd.Flags().StringVar(&opts.body, "body", "", "pull request body, or the intent to render into the template")
	cmd.Flags().StringVar(&opts.bodyFile, "body-file", "", "read the body from a file")
	cmd.Flags().StringVar(&opts.base, "base", "", "base branch (defaults to the repository's default branch)")

	return cmd
}

func runShip(ctx context.Context, root *Options, opts *shipOptions, out io.Writer) error {
	resolved, err := appconfig.Load(ctx, "")
	if err != nil {
		return err
	}

	commitStyle := resolved.Config.Commit.Style
	validator, styleErr := style.For(commitStyle)
	if styleErr != nil {
		return fail(styleErr.Error(), "set commit.style to conventional-commits, gitmoji, or freeform-with-rules")
	}

	// The pr-side flags are validated before anything mutates: a mistyped body
	// flag or an unreadable template must fail before a branch or commit exists.
	prOpts := &prOptions{title: opts.title, body: opts.body, bodyFile: opts.bodyFile, base: opts.base}
	body, err := suppliedBody(prOpts)
	if err != nil {
		return err
	}

	template, err := pr.LoadTemplate(resolved.Config.PR.Template)
	if err != nil {
		return fail(
			fmt.Sprintf("cannot read pr template %s", resolved.Config.PR.Template),
			"point pr.template at a readable file, or unset it to use the built-in template",
		)
	}

	repo := gitrepo.New("")
	provider := pr.NewGH("")

	status, err := repo.Status(ctx)
	if err != nil {
		return err
	}

	currentBranch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return err
	}

	base := opts.base
	if base == "" {
		base, err = defaultBranch(ctx, resolved, repo, provider, root.DryRun)
		if err != nil {
			return err
		}
	}
	onDefault := currentBranch == base

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
	committing := len(files) > 0

	branch := currentBranch
	createdBranch := false
	reuseBranch := false
	message := ""

	if committing {
		// The message resolves before any branch is created: the branch name
		// derives from it, and a failed resolve must leave nothing mutated.
		message = opts.message
		if message == "" || !validator.Validate(message).Valid {
			message, err = resolveMessage(ctx, resolved, validator, commitStyle, repo, files, opts.message, out)
			if err != nil || message == "" {
				return err
			}
		}

		if onDefault {
			derived := branchpkg.Name(resolved.Config.Branch.Pattern, validator.Type(message), validator.Subject(message))
			branch, reuseBranch, err = resolveBranchName(ctx, repo, derived, base)
			if err != nil {
				return err
			}
			createdBranch = !reuseBranch
		}
	} else if onDefault {
		// Nothing staged on the default branch: there is no work to branch or
		// open a pull request for, so this fails exactly as commit does.
		return emptyStageError(status)
	}

	if root.DryRun {
		title, err := shipTitle(ctx, prOpts, repo, branch, message, committing)
		if err != nil {
			return err
		}
		return writeShipPayload(out, root.JSON, shipPayload{
			Message:       message,
			Branch:        branch,
			Base:          base,
			Title:         title,
			CreatedBranch: createdBranch,
			DryRun:        true,
		})
	}

	if committing {
		if onDefault {
			if reuseBranch {
				if err := repo.Checkout(ctx, branch); err != nil {
					return err
				}
			} else if err := repo.CreateBranch(ctx, branch); err != nil {
				return err
			}
		}
		if err := repo.Commit(ctx, message); err != nil {
			return err
		}
	} else {
		// A feature branch with nothing staged is a resume only if it carries
		// commits of its own; otherwise the sole path to success is an already
		// open pull request, and anything else is the genuine empty case.
		work, err := aheadOfBase(ctx, repo, currentBranch, base)
		if err != nil {
			return err
		}
		if !work {
			existing, found, err := provider.ExistingPR(ctx, branch)
			if err != nil {
				return err
			}
			if found {
				if err := baseMismatch(opts.base, existing.Base); err != nil {
					return err
				}
				return writeShipPayload(out, root.JSON, shipPayload{URL: existing.URL, Branch: branch, Base: base, Existing: true})
			}
			return emptyStageError(status)
		}
	}

	return openPR(ctx, root, prOpts, resolved, repo, provider, template, out, branch, base, body, message, committing, createdBranch)
}

func openPR(
	ctx context.Context,
	root *Options,
	prOpts *prOptions,
	resolved appconfig.Resolved,
	repo gitrepo.Repository,
	provider pr.Provider,
	template pr.Template,
	out io.Writer,
	branch string,
	base string,
	body string,
	message string,
	committing bool,
	createdBranch bool,
) error {
	needsPush, err := repo.NeedsPush(ctx, branch)
	if err != nil {
		return err
	}
	if needsPush {
		if err := repo.Push(ctx, "origin", branch, true); err != nil {
			return err
		}
	}

	existing, found, err := provider.ExistingPR(ctx, branch)
	if err != nil {
		return err
	}
	if found {
		if err := baseMismatch(prOpts.base, existing.Base); err != nil {
			return err
		}
		return writeShipPayload(out, root.JSON, shipPayload{
			URL:           existing.URL,
			Branch:        branch,
			Base:          base,
			CreatedBranch: createdBranch,
			Existing:      true,
			Pushed:        needsPush,
		})
	}

	title, err := shipTitle(ctx, prOpts, repo, branch, message, committing)
	if err != nil {
		return err
	}

	body, err = resolveBody(ctx, resolved, repo, template, body, title, base)
	if err != nil {
		return err
	}

	url, err := provider.OpenPR(ctx, pr.Request{Base: base, Head: branch, Title: title, Body: body})
	if err != nil {
		return err
	}

	return writeShipPayload(out, root.JSON, shipPayload{
		Message:       message,
		Branch:        branch,
		Base:          base,
		Title:         title,
		URL:           url,
		CreatedBranch: createdBranch,
		Pushed:        needsPush,
	})
}

// resolveBranchName settles a collision on the default branch, walking the base
// name then -2, -3, … A candidate that does not exist is created fresh; one
// whose tip is an ancestor of HEAD is this command's own earlier partial run
// and is reused; any other existing branch belongs to unrelated work, so the
// search moves on. The default branch itself is never a candidate — reusing it
// would commit onto the branch ship exists to keep commits off — so a derived
// name equal to base is suffixed past.
func resolveBranchName(ctx context.Context, repo gitrepo.Repository, name string, base string) (string, bool, error) {
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", name, i)
		}
		if candidate == base {
			continue
		}

		exists, err := repo.BranchExists(ctx, candidate)
		if err != nil {
			return "", false, err
		}
		if !exists {
			return candidate, false, nil
		}

		ancestor, err := repo.IsAncestor(ctx, candidate, "HEAD")
		if err != nil {
			return "", false, err
		}
		if ancestor {
			return candidate, true, nil
		}
	}
}

// shipTitle is the pull request title. When this run commits, the branch's new
// tip is the resolved message, so the title is its subject without depending on
// the commit having happened yet — which lets --dry-run preview it. A resume
// reads the existing tip through the shared prTitle helper.
func shipTitle(ctx context.Context, prOpts *prOptions, repo gitrepo.Repository, branch string, message string, committing bool) (string, error) {
	if prOpts.title != "" {
		return prOpts.title, nil
	}
	if committing {
		header, _, _ := strings.Cut(message, "\n")
		return strings.TrimSuffix(header, "\r"), nil
	}
	return prTitle(ctx, prOpts, repo, branch)
}

// aheadOfBase reports whether the branch carries commits the base does not. The
// base branch often has no local ref — a fresh clone may never have fetched it —
// so a resolvable base is measured directly, and an unresolvable one falls back
// to the fork point against the other local branches.
//
// When there is no other local branch, there is no reference point at all: the
// branch is effectively its own trunk, so it is treated as not ahead. Counting
// all of its history as "ahead" instead — which an empty branch set would do —
// would let ship open a duplicate pull request for work that is already merged.
func aheadOfBase(ctx context.Context, repo gitrepo.Repository, currentBranch string, base string) (bool, error) {
	for _, ref := range []string{"origin/" + base, base} {
		if subjects, err := repo.Subjects(ctx, 1, ref+"..HEAD"); err == nil {
			return len(subjects) > 0, nil
		}
	}

	branches, err := repo.LocalBranches(ctx)
	if err != nil {
		return false, err
	}
	revs := []string{"HEAD", "--not"}
	for _, b := range branches {
		if b != currentBranch {
			revs = append(revs, b)
		}
	}
	if len(revs) == 2 { // only HEAD and --not: no other branch to compare against
		return false, nil
	}

	subjects, err := repo.Subjects(ctx, 1, revs...)
	if err != nil {
		return false, err
	}
	return len(subjects) > 0, nil
}

func writeShipPayload(out io.Writer, asJSON bool, payload shipPayload) error {
	if asJSON {
		return json.NewEncoder(out).Encode(payload)
	}

	switch {
	case payload.DryRun:
		_, _ = fmt.Fprintf(out, "dry-run: would ship %s -> %s\n", payload.Branch, payload.Base)
		_, _ = fmt.Fprintf(out, "message: %s\n", payload.Message)
		_, _ = fmt.Fprintf(out, "title: %s\n", payload.Title)
		return nil
	case payload.Existing:
		_, _ = fmt.Fprintln(out, "pull request already open")
	default:
		_, _ = fmt.Fprintln(out, "shipped")
	}
	_, _ = fmt.Fprintln(out, payload.URL)
	return nil
}
