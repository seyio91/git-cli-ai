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
	"github.com/seyio91/git-cli-ai/internal/memory"
	"github.com/seyio91/git-cli-ai/internal/pr"
	"github.com/spf13/cobra"
)

type prOptions struct {
	title    string
	body     string
	bodyFile string
	intent   string
	base     string
	draft    bool
	update   bool
}

type prPayload struct {
	URL      string `json:"url"`
	Branch   string `json:"branch"`
	Base     string `json:"base"`
	Title    string `json:"title"`
	Existing bool   `json:"existing,omitempty"`
	// Updated reports that this run rewrote the open pull request's prose. It
	// sits alongside Existing rather than replacing it: the pull request was
	// still found rather than created, and a caller that only wants to know
	// whether it had to create one should not have to learn a second field.
	Updated bool `json:"updated,omitempty"`
	// Draft reports the state this run asked the forge for. It is only ever set
	// on a run that created the pull request: converging on an existing one
	// reports existing instead, because nothing was created to be a draft.
	Draft bool `json:"draft,omitempty"`
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
	cmd.Flags().StringVar(&opts.body, "body", "", "pull request body, used verbatim")
	cmd.Flags().StringVar(&opts.bodyFile, "body-file", "", "read the verbatim body from a file")
	cmd.Flags().StringVar(&opts.intent, "intent", "", "describe the change and let the provider render it into the template")
	cmd.Flags().StringVar(&opts.base, "base", "", "base branch (defaults to the repository's default branch)")
	cmd.Flags().BoolVar(&opts.draft, "draft", false, "open the pull request as a draft")
	cmd.Flags().BoolVar(&opts.update, "update", false, "rewrite the body of an already-open pull request")

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
		base, err = defaultBranch(ctx, resolved, repo, provider, root.DryRun)
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

	existing, found, err := provider.ExistingPR(ctx, branch)
	if err != nil {
		return err
	}
	if found {
		if err := baseMismatch(opts.base, existing.Base); err != nil {
			return err
		}
		updated, err := updateExisting(ctx, root, opts, resolved, repo, provider, template, branch, base, body)
		if err != nil {
			return err
		}
		return writePRPayload(out, root.JSON, prPayload{
			URL:      existing.URL,
			Branch:   branch,
			Base:     base,
			Title:    opts.title,
			Existing: true,
			Updated:  updated,
			Pushed:   needsPush,
			DryRun:   root.DryRun && updated,
		})
	}

	title, err := prTitle(ctx, opts, repo, branch)
	if err != nil {
		return err
	}

	// The dry-run return sits before resolveBody: a preview must cost no
	// provider call, and the body it would generate is not part of the preview.
	if root.DryRun {
		return writePRPayload(out, root.JSON, prPayload{Branch: branch, Base: base, Title: title, Draft: opts.draft, DryRun: true})
	}

	body, err = resolveBody(ctx, resolved, repo, template, body, opts.intent, title, base)
	if err != nil {
		return err
	}

	url, err := provider.OpenPR(ctx, pr.Request{Base: base, Head: branch, Title: title, Body: body, Draft: opts.draft})
	if err != nil {
		return err
	}

	return writePRPayload(out, root.JSON, prPayload{URL: url, Branch: branch, Base: base, Title: title, Draft: opts.draft})
}

// suppliedBody resolves the body flags into one string. They are mutually
// exclusive: silently preferring one would make a scripted caller's mistake
// invisible. --intent contradicts both of them outright — one says "use this
// text", the other says "write something from this text" — so supplying both is
// a caller error rather than a precedence question.
func suppliedBody(opts *prOptions) (string, error) {
	if opts.body != "" && opts.bodyFile != "" {
		return "", fail(
			"--body and --body-file are mutually exclusive",
			"pass the body inline with --body, or its path with --body-file, but not both",
		)
	}

	if opts.intent != "" && (opts.body != "" || opts.bodyFile != "") {
		supplied := "--body"
		if opts.bodyFile != "" {
			supplied = "--body-file"
		}
		return "", fail(
			fmt.Sprintf("%s and --intent are mutually exclusive", supplied),
			fmt.Sprintf("%s is used verbatim, while --intent is written up for you; pass whichever you meant, not both", supplied),
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
func defaultBranch(ctx context.Context, resolved appconfig.Resolved, repo gitrepo.Repository, provider pr.Provider, dryRun bool) (string, error) {
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

	// Best effort, and skipped under --dry-run: the cache is an optimisation,
	// and a preview must write nothing — caching origin/HEAD is a real ref
	// mutation.
	if !dryRun {
		_ = repo.SetRemoteHead(ctx, "origin", branch)
	}

	return branch, nil
}

// baseMismatch errors when the caller explicitly asked for a base that
// disagrees with an existing open PR's base. An unset requested base (the
// detected default) or an unknown existing base is not a disagreement — those
// converge, returning the existing URL.
func baseMismatch(requested, existing string) error {
	if requested == "" || existing == "" || requested == existing {
		return nil
	}
	return fail(
		fmt.Sprintf("an open pull request for this branch targets %s, but --base is %s", existing, requested),
		fmt.Sprintf("re-run without --base to use the existing pull request, or close it to open one against %s", requested),
	)
}

// updateExisting handles a branch that already has an open pull request, and
// reports whether this run rewrote it. Three outcomes: a bare re-run converges
// exactly as it always has; prose without --update is refused; --update writes
// the body, and the title too when --title named one.
//
// --update is only consulted here, so asking for one on a branch with no open
// pull request opens it instead of failing. Converging on the asked-for end
// state is what the rest of this command does, and an agent that cannot know
// whether the pull request exists yet should not have to.
func updateExisting(
	ctx context.Context,
	root *Options,
	opts *prOptions,
	resolved appconfig.Resolved,
	repo gitrepo.Repository,
	provider pr.Provider,
	template pr.Template,
	branch string,
	base string,
	supplied string,
) (bool, error) {
	if !opts.update {
		return false, proseWithoutUpdate(opts)
	}

	// A preview reports intent only. Generating the body would cost a provider
	// call, which --dry-run promises not to make, and fetching the current one
	// to diff against it is a larger feature than this needs to be.
	if root.DryRun {
		return true, nil
	}

	// The title is computed even when it is not being changed: resolveBody
	// takes it as generation context and as the offline summary fallback, so a
	// body written without it is written about nothing. Only an explicit
	// --title is sent on to the forge.
	title, err := prTitle(ctx, opts, repo, branch)
	if err != nil {
		return false, err
	}

	body, err := resolveBody(ctx, resolved, repo, template, supplied, opts.intent, title, base)
	if err != nil {
		return false, err
	}

	update := pr.Update{Head: branch, Body: &body}
	if opts.title != "" {
		update.Title = &opts.title
	}
	if err := provider.UpdatePR(ctx, update); err != nil {
		return false, err
	}
	return true, nil
}

// proseWithoutUpdate refuses prose aimed at a pull request that is already
// open. This used to exit 0 having sent nothing, which told an agent its body
// had landed — the missing capability read as a silent success rather than as
// a gap, and that is worse than either.
func proseWithoutUpdate(opts *prOptions) error {
	supplied := ""
	switch {
	case opts.bodyFile != "":
		supplied = "--body-file"
	case opts.body != "":
		supplied = "--body"
	case opts.intent != "":
		supplied = "--intent"
	case opts.title != "":
		supplied = "--title"
	default:
		return nil
	}

	return fail(
		fmt.Sprintf("a pull request is already open for this branch, so %s would be ignored", supplied),
		fmt.Sprintf("add --update to rewrite it, or drop %s to converge on the open pull request", supplied),
	)
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

// resolveBody applies the settled rule: a supplied body is authoritative and is
// used exactly as given; --intent is text to be written up into the template,
// grounded on what it says; neither is generated from the diff alone.
//
// An authoritative body returns before anything reads the config's provider,
// resolves a generator, or touches the network. That is the point of it: a
// caller who has already decided what the pull request says should not need a
// reachable provider — or a VPN, or a valid key — to post it. The heading-match
// heuristic this replaced could reclassify a finished hand-written body as
// intent and silently discard sections of it, which is a correctness failure,
// not a style one.
//
// The asymmetry with commit's conformance gate is deliberate. A commit message
// is checked against conventional-commits, a crisp and checkable spec, so
// "does this conform" has a real answer. A pull request body's only structure is
// its headings, and matching those is far too weak a signal to justify
// overwriting prose a person wrote.
func resolveBody(
	ctx context.Context,
	resolved appconfig.Resolved,
	repo gitrepo.Repository,
	template pr.Template,
	supplied string,
	intent string,
	title string,
	base string,
) (string, error) {
	if supplied != "" {
		return supplied, nil
	}

	mem := memoryContext(ctx, repo)

	// Same rule as commit: the provider is only engaged when a layer actually
	// asked for one. With none configured the body is rendered locally rather
	// than failing — an unopenable pull request is a worse outcome than a
	// terse one.
	if resolved.Sources["ai.provider"] == appconfig.LayerDefault {
		return offlineBody(ctx, repo, template, intent, title, base, mem)
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
		Context: withMemory(prContext(ctx, repo, title, base), mem),
		Intent:  intent,
	})
	if err != nil {
		return "", err
	}
	return pr.StripPlaceholders(result.Message), nil
}

// offlineBody fills the template from the commit log. Placeholders with no
// local source resolve to nothing, which the template is required to render
// cleanly. {{task}} and {{plan}} are still supplied even though the default
// template no longer asks for them: a repository that wants them names them in
// its own pr.template.
func offlineBody(ctx context.Context, repo gitrepo.Repository, template pr.Template, intent string, title string, base string, mem memory.Context) (string, error) {
	subjects := branchSubjects(ctx, repo, base)

	summary := intent
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
		"task":    mem.Task,
		"plan":    mem.Plan,
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

// draftLabel prefixes a dry-run line so a preview says which state it would open
// in. It carries its own trailing space so the non-draft case adds nothing.
func draftLabel(draft bool) string {
	if draft {
		return "draft "
	}
	return ""
}

func writePRPayload(out io.Writer, asJSON bool, payload prPayload) error {
	if asJSON {
		return json.NewEncoder(out).Encode(payload)
	}

	switch {
	case payload.DryRun && payload.Updated:
		_, _ = fmt.Fprintf(out, "dry-run: would update the pull request for %s\n", payload.Branch)
		_, _ = fmt.Fprintln(out, payload.URL)
		return nil
	case payload.DryRun:
		_, _ = fmt.Fprintf(out, "dry-run: would open %s%s -> %s\n", draftLabel(payload.Draft), payload.Branch, payload.Base)
		_, _ = fmt.Fprintf(out, "title: %s\n", payload.Title)
		return nil
	case payload.Updated:
		_, _ = fmt.Fprintln(out, "pull request updated")
	case payload.Existing:
		_, _ = fmt.Fprintln(out, "pull request already open")
	case payload.Draft:
		_, _ = fmt.Fprintln(out, "draft pull request opened")
	default:
		_, _ = fmt.Fprintln(out, "pull request opened")
	}
	_, _ = fmt.Fprintln(out, payload.URL)
	return nil
}
