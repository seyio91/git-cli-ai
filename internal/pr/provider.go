package pr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Request is everything a forge needs to open a pull request. It is
// deliberately forge-neutral so Bitbucket or Azure can implement Provider
// without the command layer learning anything about them.
type Request struct {
	Base  string
	Head  string
	Title string
	Body  string
	// Draft opens the pull request in whatever "not ready for review" state the
	// forge offers. Every forge worth supporting has one, so it belongs here
	// rather than in a GitHub-specific field.
	Draft bool
}

// Existing describes an open pull request already on a branch. Base may be
// empty when the forge does not report it; callers must treat an empty Base as
// "unknown", not as a mismatch.
type Existing struct {
	URL  string
	Base string
}

// Update describes a change to a pull request that is already open. A nil field
// means "leave it as it is", which is why these are pointers: a body can
// legitimately be set to something, and the absence of an instruction has to be
// distinguishable from an instruction to write nothing.
//
// There is no Base field. Re-pointing an open pull request at a different base
// is a far larger action than rewording it, and baseMismatch deliberately treats
// a base change as an error rather than something to apply.
type Update struct {
	Head  string
	Title *string
	Body  *string
}

// Provider is the forge-facing surface. There is no merge method, and there
// will not be one: refusing to merge is the property this tool is built around.
// UpdatePR edits an open pull request's prose; that is the closest this
// interface comes to acting on one, and it is still not a merge.
type Provider interface {
	// ExistingPR returns the open pull request for head, if one exists. A false
	// second result means none was found, not an error.
	ExistingPR(ctx context.Context, head string) (Existing, bool, error)
	DefaultBranch(ctx context.Context) (string, error)
	OpenPR(ctx context.Context, req Request) (string, error)
	// UpdatePR changes the title and/or body of the open pull request on
	// req.Head. A nil field is left untouched.
	UpdatePR(ctx context.Context, req Update) error
}

// Error reports a forge failure. Hint is actionable guidance; the tool's own
// words go in Details, mirroring the git and provider error contracts.
type Error struct {
	Message string
	Hint    string
	Details string
}

func (e *Error) Error() string {
	return e.Message
}

// commandTimeout bounds a gh call that never returns — an unauthenticated gh
// can sit waiting on a prompt, and an agent hot path must not block forever.
const commandTimeout = 60 * time.Second

// GH drives the GitHub CLI. Every invocation is argv passed straight to exec:
// no shell, so a branch name containing a semicolon cannot become a second
// command.
type GH struct {
	Dir    string
	Binary string
}

func NewGH(dir string) GH {
	return GH{Dir: dir, Binary: "gh"}
}

func (g GH) ExistingPR(ctx context.Context, head string) (Existing, bool, error) {
	out, err := g.run(ctx, "", "pr", "list", "--head", head, "--state", "open", "--json", "url,baseRefName", "--limit", "1")
	if err != nil {
		return Existing{}, false, err
	}

	var prs []struct {
		URL         string `json:"url"`
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &prs); err != nil {
		return Existing{}, false, &Error{
			Message: "could not read the pull request list from gh",
			Hint:    "run `gh pr list --head " + head + " --json url,baseRefName` to see what it emits",
			Details: strings.TrimSpace(out),
		}
	}

	if len(prs) == 0 || prs[0].URL == "" {
		return Existing{}, false, nil
	}
	return Existing{URL: prs[0].URL, Base: prs[0].BaseRefName}, true, nil
}

func (g GH) DefaultBranch(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "", "repo", "view", "--json", "defaultBranchRef")
	if err != nil {
		return "", err
	}

	var view struct {
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &view); err != nil {
		return "", &Error{
			Message: "could not read the default branch from gh",
			Hint:    "run `gh repo view --json defaultBranchRef` to see what it emits, or set branch.default_branch in .git-cli.toml",
			Details: strings.TrimSpace(out),
		}
	}
	if view.DefaultBranchRef.Name == "" {
		return "", &Error{
			Message: "gh reported no default branch for this repository",
			Hint:    "set branch.default_branch in .git-cli.toml",
		}
	}
	return view.DefaultBranchRef.Name, nil
}

// OpenPR creates the pull request and returns its URL. The body travels on
// stdin via `--body-file -` so a long body never has to survive an argv limit.
func (g GH) OpenPR(ctx context.Context, req Request) (string, error) {
	args := []string{
		"pr", "create",
		"--base", req.Base,
		"--head", req.Head,
		"--title", req.Title,
		"--body-file", "-",
	}
	if req.Draft {
		args = append(args, "--draft")
	}

	out, err := g.run(ctx, req.Body, args...)
	if err != nil {
		return "", err
	}

	url := lastLine(out)
	if url == "" {
		return "", &Error{
			Message: "gh created the pull request but printed no URL",
			Hint:    "run `gh pr view --json url` to find it",
		}
	}
	return url, nil
}

// UpdatePR edits an open pull request. The body travels on stdin via
// `--body-file -` for the same reason OpenPR does: a long body must not have to
// survive an argv limit. The pull request is addressed by its branch, which is
// how ExistingPR already finds it, so no number has to be threaded through.
//
// A request with nothing set is a no-op rather than an error. The command layer
// only calls this when an update was asked for, so an empty one means a caller
// bug, not a user mistake — and issuing `gh pr edit` with no flags would edit
// nothing while still costing a round trip and a chance to fail.
func (g GH) UpdatePR(ctx context.Context, req Update) error {
	if req.Title == nil && req.Body == nil {
		return nil
	}

	args := []string{"pr", "edit", req.Head}
	if req.Title != nil {
		args = append(args, "--title", *req.Title)
	}

	var stdin string
	if req.Body != nil {
		args = append(args, "--body-file", "-")
		stdin = *req.Body
	}

	_, err := g.run(ctx, stdin, args...)
	return err
}

func (g GH) run(ctx context.Context, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	binary := g.Binary
	if binary == "" {
		binary = "gh"
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", &Error{
				Message: fmt.Sprintf("gh %s timed out after %s", label(args), commandTimeout),
				Hint:    "run `gh auth status` — an unauthenticated gh can block waiting for input",
			}
		}
		return "", &Error{
			Message: fmt.Sprintf("gh %s failed", label(args)),
			Hint:    ghHint(err, stderr.String()),
			Details: strings.TrimSpace(stderr.String()),
		}
	}

	return stdout.String(), nil
}

// label names the subcommand without its arguments, which is all an error
// message needs and keeps branch names and titles out of it.
func label(args []string) string {
	if len(args) > 2 {
		args = args[:2]
	}
	return strings.Join(args, " ")
}

func ghHint(err error, stderr string) string {
	lowered := strings.ToLower(stderr)

	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "install the GitHub CLI and ensure `gh` is on PATH"
	case strings.Contains(lowered, "auth"), strings.Contains(lowered, "not logged in"):
		return "authenticate with `gh auth login`"
	case strings.Contains(lowered, "could not determine"), strings.Contains(lowered, "no git remotes"):
		return "run this inside a repository with a GitHub remote, or add one with `git remote add origin <url>`"
	case strings.Contains(lowered, "permission"), strings.Contains(lowered, "403"):
		return "the authenticated account cannot open a pull request on this repository; check its permissions"
	}

	return "run the same gh command manually to see its full output"
}

// lastLine is what reads the URL out of gh's output: gh prints progress lines
// before the URL on some paths, and the URL is always last.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
