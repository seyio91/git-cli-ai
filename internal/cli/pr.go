package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/seyio91/git-cli-ai/internal/ai"
	appconfig "github.com/seyio91/git-cli-ai/internal/config"
	gitrepo "github.com/seyio91/git-cli-ai/internal/git"
	"github.com/seyio91/git-cli-ai/internal/pr"
	"github.com/spf13/cobra"
)

type prOptions struct {
	title    string
	body     string
	bodyFile string
	base     string
}

type prPayload struct {
	URL      string `json:"url"`
	Branch   string `json:"branch"`
	Base     string `json:"base"`
	Title    string `json:"title"`
	Existing bool   `json:"existing,omitempty"`
	// Pushed reports whether this run moved commits to the remote, so a caller
	// can tell a converged re-run from a no-op one.
	Pushed bool `json:"pushed,omitempty"`
	DryRun bool `json:"dry_run,omitempty"`
}

func NewPRCommand(root *Options, out io.Writer) *cobra.Command {
	opts := &prOptions{}
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Open a pull request for the current branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPR(cmd.Context(), root, opts, out)
		},
	}

	cmd.Flags().StringVar(&opts.title, "title", "", "pull request title")
	cmd.Flags().StringVar(&opts.body, "body", "", "pull request body, or the intent to render into the template")
	cmd.Flags().StringVar(&opts.bodyFile, "body-file", "", "read the body from a file")
	cmd.Flags().StringVar(&opts.base, "base", "", "base branch (defaults to the repository's default branch)")

	return cmd
}

func runPR(ctx context.Context, root *Options, opts *prOptions, out io.Writer) error {
	resolved, err := appconfig.Load(ctx, "")
	if err != nil {
		return err
	}

	// Body flags are validated before anything else so misuse can never reach
	// the forge: an empty body must not silently become a pull request.
	body, err := suppliedBody(opts)
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
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return err
	}

	provider := pr.NewGH("")

	base := opts.base
	if base == "" {
		base, err = defaultBranch(ctx, resolved, repo, provider)
		if err != nil {
			return err
		}
	}

	if branch == base {
		return fail(
			fmt.Sprintf("%s is the default branch", branch),
			fmt.Sprintf("check out a feature branch with `git checkout -b <name>` and re-run; pr never moves commits off %s", base),
		)
	}

	// Pushing before the existing-PR check is what makes a re-run actually
	// converge. Returning the URL early while local commits sit unpushed
	// reports success for a PR that does not contain the work: the agent fixes
	// review feedback, re-runs, gets exit 0, and the branch never moves.
	needsPush, err := repo.NeedsPush(ctx, branch)
	if err != nil {
		return err
	}
	if needsPush && !root.DryRun {
		if err := repo.Push(ctx, "origin", branch, true); err != nil {
			return err
		}
	}

	url, found, err := provider.ExistingPR(ctx, branch)
	if err != nil {
		return err
	}
	if found {
		return writePRPayload(out, root.JSON, prPayload{
			URL:      url,
			Branch:   branch,
			Base:     base,
			Existing: true,
			Pushed:   needsPush,
		})
	}

	title, err := prTitle(ctx, opts, repo, branch)
	if err != nil {
		return err
	}

	body, err = resolveBody(ctx, resolved, repo, template, body, title, base)
	if err != nil {
		return err
	}

	if root.DryRun {
		return writePRPayload(out, root.JSON, prPayload{Branch: branch, Base: base, Title: title, DryRun: true})
	}

	url, err = provider.OpenPR(ctx, pr.Request{Base: base, Head: branch, Title: title, Body: body})
	if err != nil {
		return err
	}

	return writePRPayload(out, root.JSON, prPayload{URL: url, Branch: branch, Base: base, Title: title})
}

// suppliedBody resolves the two body flags into one string. They are mutually
// exclusive: silently preferring one would make a scripted caller's mistake
// invisible.
func suppliedBody(opts *prOptions) (string, error) {
	if opts.body != "" && opts.bodyFile != "" {
		return "", fail(
			"--body and --body-file are mutually exclusive",
			"pass the body inline with --body, or its path with --body-file, but not both",
		)
	}

	if opts.bodyFile == "" {
		return opts.body, nil
	}

	data, err := os.ReadFile(opts.bodyFile)
	if err != nil {
		return "", failWithDetails(
			fmt.Sprintf("cannot read --body-file %s", opts.bodyFile),
			fmt.Sprintf("check that %s exists and is readable", opts.bodyFile),
			err.Error(),
		)
	}
	return string(data), nil
}

// defaultBranch resolves the base branch in precedence order: an explicit
// config value, then origin/HEAD, then gh. The config value short-circuits
// before any forge call, and a branch learned from gh is cached back into
// origin/HEAD so the next run takes the offline path.
func defaultBranch(ctx context.Context, resolved appconfig.Resolved, repo gitrepo.Repository, provider pr.Provider) (string, error) {
	if configured := resolved.Config.Branch.DefaultBranch; configured != "" {
		return configured, nil
	}

	if head, err := repo.RemoteHead(ctx, "origin"); err == nil && head != "" {
		return head, nil
	}

	branch, err := provider.DefaultBranch(ctx)
	if err != nil {
		return "", err
	}

	// Best effort: the cache is an optimisation, and a remote that cannot be
	// reached to record it must not fail a pull request that otherwise works.
	_ = repo.SetRemoteHead(ctx, "origin", branch)

	return branch, nil
}

// prTitle uses the branch's most recent commit subject. It needs no base ref,
// which matters because the base branch often has no local ref at all.
func prTitle(ctx context.Context, opts *prOptions, repo gitrepo.Repository, branch string) (string, error) {
	if opts.title != "" {
		return opts.title, nil
	}

	subjects, err := repo.Subjects(ctx, 1)
	if err != nil {
		return "", err
	}
	if len(subjects) == 0 {
		return branch, nil
	}
	return subjects[0], nil
}

// resolveBody applies the settled rule: a supplied body that already fills the
// template is used verbatim and costs nothing; one that does not is intent to
// be rendered into the template, grounded on that text; no body at all is
// generated from the diff.
func resolveBody(
	ctx context.Context,
	resolved appconfig.Resolved,
	repo gitrepo.Repository,
	template pr.Template,
	supplied string,
	title string,
	base string,
) (string, error) {
	if supplied != "" && template.Fills(supplied) {
		return supplied, nil
	}

	// Same rule as commit: the provider is only engaged when a layer actually
	// asked for one. With none configured the body is rendered locally rather
	// than failing — an unopenable pull request is a worse outcome than a
	// terse one.
	if resolved.Sources["ai.provider"] == appconfig.LayerDefault {
		return offlineBody(ctx, repo, template, supplied, title, base)
	}

	diff, err := branchDiff(ctx, repo, base)
	if err != nil {
		return "", err
	}

	generator, err := ai.NewFor(resolved.Config, ai.KindPRBody)
	if err != nil {
		return "", err
	}

	result, err := generator.Generate(ctx, ai.GenRequest{
		Kind:    ai.KindPRBody,
		Style:   template.Text,
		Diff:    diff,
		Context: prContext(ctx, repo, title, base),
		Intent:  supplied,
	})
	if err != nil {
		return "", err
	}
	return result.Message, nil
}

// offlineBody fills the template from the commit log. Placeholders with no
// local source resolve to nothing, which the template is required to render
// cleanly.
func offlineBody(ctx context.Context, repo gitrepo.Repository, template pr.Template, supplied string, title string, base string) (string, error) {
	subjects := branchSubjects(ctx, repo, base)

	summary := supplied
	if summary == "" {
		summary = title
	}

	var changes strings.Builder
	for _, subject := range subjects {
		changes.WriteString("- ")
		changes.WriteString(subject)
		changes.WriteString("\n")
	}

	return template.Render(map[string]string{
		"summary": summary,
		"changes": strings.TrimRight(changes.String(), "\n"),
	}), nil
}

// branchDiff is the change the pull request proposes: everything on this branch
// since it left the base. The base often has no local ref — a fresh clone's
// origin/<base> may be absent and the local branch may never have existed — so
// a missing ref yields an empty diff rather than an error, leaving the commit
// subjects to ground the generation.
func branchDiff(ctx context.Context, repo gitrepo.Repository, base string) (string, error) {
	for _, ref := range []string{"origin/" + base, base} {
		diff, err := repo.DiffRange(ctx, ref)
		if err == nil {
			return diff, nil
		}
	}
	return "", nil
}

// branchSubjects lists this branch's own commits. It degrades the same way
// branchDiff does — an unresolvable base falls back to recent history, which is
// a superset rather than nothing.
func branchSubjects(ctx context.Context, repo gitrepo.Repository, base string) []string {
	for _, ref := range []string{"origin/" + base, base} {
		if subjects, err := repo.Subjects(ctx, subjectLimit, ref+"..HEAD"); err == nil {
			return subjects
		}
	}

	subjects, _ := repo.Subjects(ctx, subjectLimit)
	return subjects
}

// subjectLimit caps how much history reaches a prompt or a body. A branch with
// more commits than this is not one whose every subject belongs in a summary.
const subjectLimit = 20

func prContext(ctx context.Context, repo gitrepo.Repository, title string, base string) []ai.ContextBlock {
	blocks := []ai.ContextBlock{{Label: "title", Content: title}}

	if subjects := branchSubjects(ctx, repo, base); len(subjects) > 0 {
		blocks = append(blocks, ai.ContextBlock{Label: "commits", Content: strings.Join(subjects, "\n")})
	}
	if branch, err := repo.CurrentBranch(ctx); err == nil {
		blocks = append(blocks, ai.ContextBlock{Label: "branch", Content: branch})
	}
	return blocks
}

func writePRPayload(out io.Writer, asJSON bool, payload prPayload) error {
	if asJSON {
		return json.NewEncoder(out).Encode(payload)
	}

	switch {
	case payload.DryRun:
		_, _ = fmt.Fprintf(out, "dry-run: would open %s -> %s\n", payload.Branch, payload.Base)
		_, _ = fmt.Fprintf(out, "title: %s\n", payload.Title)
		return nil
	case payload.Existing:
		_, _ = fmt.Fprintln(out, "pull request already open")
	default:
		_, _ = fmt.Fprintln(out, "pull request opened")
	}
	_, _ = fmt.Fprintln(out, payload.URL)
	return nil
}
