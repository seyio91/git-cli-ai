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
	intent   string
	base     string
	draft    bool
	update   bool
}

type shipPayload struct {
	Message       string `json:"message,omitempty"`
	Branch        string `json:"branch"`
	Base          string `json:"base"`
	Title         string `json:"title,omitempty"`
	URL           string `json:"url,omitempty"`
	CreatedBranch bool   `json:"created_branch,omitempty"`
	Existing      bool   `json:"existing,omitempty"`
	Updated       bool   `json:"updated,omitempty"`
	Draft         bool   `json:"draft,omitempty"`
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
	cmd.Flags().StringVar(&opts.body, "body", "", "pull request body, used verbatim")
	cmd.Flags().StringVar(&opts.bodyFile, "body-file", "", "read the verbatim body from a file")
	cmd.Flags().StringVar(&opts.intent, "intent", "", "describe the change and let the provider render it into the template")
	cmd.Flags().StringVar(&opts.base, "base", "", "base branch (defaults to the repository's default branch)")
	cmd.Flags().BoolVar(&opts.draft, "draft", false, "open the pull request as a draft")
	cmd.Flags().BoolVar(&opts.update, "update", false, "rewrite the body of an already-open pull request")

	return cmd
}

func runShip(ctx context.Context, root *Options, opts *shipOptions, out io.Writer) error {
	resolved, err := appconfig.Load(ctx, "")
	if err != nil {
		return err
	}

	commitStyle := resolved.Config.Commit.Style
	validator, styleErr := style.For(commitStyle, resolved.Config.Commit.Types)
	if styleErr != nil {
		return fail(styleErr.Error(), "set commit.style to conventional-commits, gitmoji, or freeform-with-rules")
	}

	// The pr-side flags are validated before anything mutates: a mistyped body
	// flag or an unreadable template must fail before a branch or commit exists.
	prOpts := &prOptions{title: opts.title, body: opts.body, bodyFile: opts.bodyFile, intent: opts.intent, base: opts.base, draft: opts.draft, update: opts.update}
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
			Draft:         opts.draft,
			DryRun:        true,
		})
	}

	progress := &shipProgress{resume: resumeCommand(opts)}

	if committing {
		if onDefault {
			if reuseBranch {
				if err := repo.Checkout(ctx, branch); err != nil {
					return err
				}
				progress.did("checked out existing branch " + branch)
			} else {
				if err := repo.CreateBranch(ctx, branch); err != nil {
					return err
				}
				progress.did("created branch " + branch)
			}
		}
		if err := repo.Commit(ctx, message); err != nil {
			return progress.wrap(err)
		}
		progress.did("committed on " + branch)
	} else {
		// A feature branch with nothing staged is a resume only if it carries
		// commits of its own; otherwise the sole path to success is an already
		// open pull request, and anything else is the genuine empty case.
		work, err := aheadOfBase(ctx, repo, currentBranch, base)
		if err != nil {
			return progress.wrap(err)
		}
		if !work {
			existing, found, err := provider.ExistingPR(ctx, branch)
			if err != nil {
				return progress.wrap(err)
			}
			if found {
				if err := baseMismatch(opts.base, existing.Base); err != nil {
					return err
				}
				updated, err := updateExisting(ctx, root, prOpts, resolved, repo, provider, template, branch, base, body)
				if err != nil {
					return progress.wrap(err)
				}
				return writeShipPayload(out, root.JSON, shipPayload{
					URL:      existing.URL,
					Branch:   branch,
					Base:     base,
					Title:    opts.title,
					Existing: true,
					Updated:  updated,
				})
			}
			return emptyStageError(status)
		}
	}

	return openPR(ctx, root, prOpts, resolved, repo, provider, template, out, progress, branch, base, body, message, committing, createdBranch)
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
	progress *shipProgress,
	branch string,
	base string,
	body string,
	message string,
	committing bool,
	createdBranch bool,
) error {
	needsPush, err := repo.NeedsPush(ctx, branch)
	if err != nil {
		return progress.wrap(err)
	}
	if needsPush {
		if err := repo.Push(ctx, "origin", branch, true); err != nil {
			return progress.wrap(err)
		}
		progress.did("pushed " + branch + " to origin")
	}

	existing, found, err := provider.ExistingPR(ctx, branch)
	if err != nil {
		return progress.wrap(err)
	}
	if found {
		if err := baseMismatch(prOpts.base, existing.Base); err != nil {
			return err
		}
		updated, err := updateExisting(ctx, root, prOpts, resolved, repo, provider, template, branch, base, body)
		if err != nil {
			return progress.wrap(err)
		}
		return writeShipPayload(out, root.JSON, shipPayload{
			Branch:        branch,
			Base:          base,
			Title:         prOpts.title,
			URL:           existing.URL,
			CreatedBranch: createdBranch,
			Existing:      true,
			Updated:       updated,
			Pushed:        needsPush,
		})
	}

	title, err := shipTitle(ctx, prOpts, repo, branch, message, committing)
	if err != nil {
		return progress.wrap(err)
	}

	body, err = resolveBody(ctx, resolved, repo, template, body, prOpts.intent, title, base)
	if err != nil {
		return progress.wrap(err)
	}

	url, err := provider.OpenPR(ctx, pr.Request{Base: base, Head: branch, Title: title, Body: body, Draft: prOpts.draft})
	if err != nil {
		return progress.wrap(err)
	}

	return writeShipPayload(out, root.JSON, shipPayload{
		Message:       message,
		Branch:        branch,
		Base:          base,
		Title:         title,
		URL:           url,
		CreatedBranch: createdBranch,
		Draft:         prOpts.draft,
		Pushed:        needsPush,
	})
}

// shipProgress records the durable work this run has already done, so a failure
// after any of it can say so. ship is a composite of four steps and only the
// last is safe to retry: once the branch exists, the commit is written and the
// push has landed, re-running ship stages and commits a second time. The error
// text on its own read like a total failure — it named only the step that
// broke — which made a blind retry the obvious next move and the wrong one.
type shipProgress struct {
	completed []string
	resume    string
}

func (p *shipProgress) did(step string) {
	p.completed = append(p.completed, step)
}

// wrap annotates err with the progress so far. An empty list means nothing
// durable has happened yet, so there is nothing to report and the error passes
// through untouched.
func (p *shipProgress) wrap(err error) error {
	if err == nil || len(p.completed) == 0 {
		return err
	}
	return partialError{err: err, completed: p.completed, resume: p.resume}
}

// resumeCommand is the command that carries on from where ship stopped: the
// pull request half of what ship does, with every pull request flag this run was
// given. Dropping any of them would make the resume quietly produce a different
// pull request from the one that was asked for, which is the same class of silent
// difference this reporting exists to remove — so values are re-emitted rather
// than summarised, quoted because a title or a body is arbitrary text. Nothing
// executes this string; it is guidance for whoever reads the error.
//
// The commit flags are deliberately absent. By the time this matters the commit
// has already been written, and re-running it is the mistake being warned about.
func resumeCommand(opts *shipOptions) string {
	cmd := "git-cli pr"
	if opts.base != "" {
		cmd += " --base " + opts.base
	}
	if opts.draft {
		cmd += " --draft"
	}
	if opts.update {
		cmd += " --update"
	}
	if opts.title != "" {
		cmd += fmt.Sprintf(" --title %q", opts.title)
	}
	switch {
	case opts.bodyFile != "":
		cmd += fmt.Sprintf(" --body-file %q", opts.bodyFile)
	case opts.body != "":
		cmd += fmt.Sprintf(" --body %q", opts.body)
	case opts.intent != "":
		cmd += fmt.Sprintf(" --intent %q", opts.intent)
	}
	return cmd
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
		_, _ = fmt.Fprintf(out, "dry-run: would ship %s -> %s as a %spull request\n", payload.Branch, payload.Base, draftLabel(payload.Draft))
		_, _ = fmt.Fprintf(out, "message: %s\n", payload.Message)
		_, _ = fmt.Fprintf(out, "title: %s\n", payload.Title)
		return nil
	case payload.Updated:
		_, _ = fmt.Fprintln(out, "pull request updated")
	case payload.Existing:
		_, _ = fmt.Fprintln(out, "pull request already open")
	case payload.Draft:
		_, _ = fmt.Fprintln(out, "shipped as a draft")
	default:
		_, _ = fmt.Fprintln(out, "shipped")
	}
	_, _ = fmt.Fprintln(out, payload.URL)
	return nil
}
