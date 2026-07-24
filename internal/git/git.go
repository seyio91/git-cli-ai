package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type Repository struct {
	Dir    string
	Binary string
}

// ErrDetachedHead reports that HEAD does not point at a named branch. git
// itself succeeds in that case and simply prints nothing, so this is a
// condition rather than a command failure and is deliberately not a
// *CommandError.
var ErrDetachedHead = errors.New("HEAD is not on a named branch")

// ErrNoUpstream reports that a branch has no tracking ref. Like ErrDetachedHead
// it is a state the caller acts on, not a failure to report.
var ErrNoUpstream = errors.New("branch has no upstream")

type CommandError struct {
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

type Status struct {
	Entries []StatusEntry
}

type StatusEntry struct {
	Index    byte
	Worktree byte
	Path     string
	OrigPath string
}

func New(dir string) Repository {
	return Repository{Dir: dir, Binary: "git"}
}

func (r Repository) Status(ctx context.Context) (Status, error) {
	out, err := r.run(ctx, "status", "--porcelain=v1", "-z")
	if err != nil {
		return Status{}, err
	}
	return parseStatus(out), nil
}

func (r Repository) Diff(ctx context.Context, staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	return r.run(ctx, args...)
}

func (r Repository) DiffStaged(ctx context.Context) (string, error) {
	return r.Diff(ctx, true)
}

func (r Repository) DiffUnstaged(ctx context.Context) (string, error) {
	return r.Diff(ctx, false)
}

// DiffRange returns the diff of HEAD against the merge base with ref, which is
// what a pull request actually proposes: commits made on ref since the branch
// left it are not part of it.
func (r Repository) DiffRange(ctx context.Context, ref string) (string, error) {
	return r.run(ctx, "diff", ref+"...HEAD")
}

func (r Repository) Add(ctx context.Context, paths ...string) error {
	args := append([]string{"add", "--"}, paths...)
	_, err := r.run(ctx, args...)
	return err
}

func (r Repository) AddTracked(ctx context.Context) error {
	_, err := r.run(ctx, "add", "-u")
	return err
}

func (r Repository) Commit(ctx context.Context, message string) error {
	_, err := r.run(ctx, "commit", "-m", message)
	return err
}

// pushTimeout bounds the only git call that touches the network. Every other
// subprocess in the tree is bounded; an unreachable host or a credential helper
// waiting on a TTY that does not exist would otherwise block an unattended
// agent forever with no diagnostic.
const pushTimeout = 2 * time.Minute

func (r Repository) Push(ctx context.Context, remote string, branch string, setUpstream bool) error {
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()

	args := []string{"push"}
	if setUpstream {
		args = append(args, "-u")
	}
	if remote != "" {
		args = append(args, remote)
	}
	if branch != "" {
		args = append(args, branch)
	}
	_, err := r.run(ctx, args...)
	return err
}

func (r Repository) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", ErrDetachedHead
	}
	return branch, nil
}

// CreateBranch creates a new branch at the current HEAD and switches to it. The
// index and working tree come along, so changes staged before the call remain
// staged on the new branch.
func (r Repository) CreateBranch(ctx context.Context, name string) error {
	_, err := r.run(ctx, "checkout", "-b", name)
	return err
}

// Checkout switches to an existing branch.
func (r Repository) Checkout(ctx context.Context, name string) error {
	_, err := r.run(ctx, "checkout", name)
	return err
}

// BranchExists reports whether a local branch of this name exists. A clean
// "not found" (exit 1) is false, not an error; any other failure is surfaced.
func (r Repository) BranchExists(ctx context.Context, name string) (bool, error) {
	_, err := r.run(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return boolFromExit(err)
}

// IsAncestor reports whether maybeAncestor is an ancestor of ref — the test for
// "is this branch our own earlier partial run". git answers no with exit 1,
// which is a false result rather than a failure.
func (r Repository) IsAncestor(ctx context.Context, maybeAncestor string, ref string) (bool, error) {
	_, err := r.run(ctx, "merge-base", "--is-ancestor", maybeAncestor, ref)
	return boolFromExit(err)
}

// boolFromExit maps a predicate git command's result to a bool: success is
// true, a clean exit 1 is false, and any other exit is a genuine error worth
// surfacing rather than silently reading as false.
func boolFromExit(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) && cmdErr.ExitCode == 1 {
		return false, nil
	}
	return false, err
}

// LocalBranches lists every local branch, in git's default (alphabetical)
// order.
func (r Repository) LocalBranches(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if b := strings.TrimSpace(line); b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// Root returns the working tree's top level.
func (r Repository) Root(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Upstream returns the tracking ref for branch, or ErrNoUpstream when none is
// configured. git reports a missing upstream as a command failure, so the
// condition is normalised here rather than surfaced as a *CommandError.
func (r Repository) Upstream(ctx context.Context, branch string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		return "", ErrNoUpstream
	}
	upstream := strings.TrimSpace(out)
	if upstream == "" {
		return "", ErrNoUpstream
	}
	return upstream, nil
}

// NeedsPush reports whether branch has commits the remote has not seen. A
// branch with no upstream always needs pushing.
func (r Repository) NeedsPush(ctx context.Context, branch string) (bool, error) {
	upstream, err := r.Upstream(ctx, branch)
	if errors.Is(err, ErrNoUpstream) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	out, err := r.run(ctx, "rev-list", "--count", upstream+".."+branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "0", nil
}

// RemoteHead reads the branch origin/HEAD points at. It is a local ref, so this
// answers the default-branch question without any network round trip — but only
// once something has set it.
func (r Repository) RemoteHead(ctx context.Context, remote string) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	ref := "refs/remotes/" + remote + "/HEAD"
	out, err := r.run(ctx, "symbolic-ref", "--short", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(out), remote+"/"), nil
}

// SetRemoteHead caches a default branch discovered elsewhere into origin/HEAD so
// the next run takes the offline path.
func (r Repository) SetRemoteHead(ctx context.Context, remote string, branch string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := r.run(ctx, "remote", "set-head", remote, branch)
	return err
}

// Subjects returns the subject lines of the most recent commits, newest first.
// A non-empty revs narrows the range — "<base>..HEAD" for just this branch's
// work — and an empty one walks back from HEAD.
func (r Repository) Subjects(ctx context.Context, limit int, revs ...string) ([]string, error) {
	args := append([]string{"log", fmt.Sprintf("-%d", limit), "--format=%s"}, revs...)
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var subjects []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			subjects = append(subjects, line)
		}
	}
	return subjects, nil
}

func (r Repository) Remote(ctx context.Context, name string) (string, error) {
	if name == "" {
		name = "origin"
	}
	out, err := r.run(ctx, "remote", "get-url", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (s Status) StagedFiles() []string {
	var files []string
	for _, entry := range s.Entries {
		if entry.IsStaged() {
			files = append(files, entry.Path)
		}
	}
	return uniqueSorted(files)
}

func (s Status) UnstagedFiles() []string {
	var files []string
	for _, entry := range s.Entries {
		if entry.IsUnstaged() {
			files = append(files, entry.Path)
		}
	}
	return uniqueSorted(files)
}

func (s Status) CommitCandidateFiles(includeTrackedUnstaged bool) []string {
	var files []string
	for _, entry := range s.Entries {
		if entry.IsStaged() || includeTrackedUnstaged && entry.IsTrackedUnstaged() {
			files = append(files, entry.Path)
		}
	}
	return uniqueSorted(files)
}

func (s Status) HasOnlyUntrackedChanges() bool {
	if len(s.Entries) == 0 {
		return false
	}
	for _, entry := range s.Entries {
		if !entry.IsUntracked() {
			return false
		}
	}
	return true
}

func (e StatusEntry) IsStaged() bool {
	return e.Index != ' ' && e.Index != '?' && e.Index != '!'
}

func (e StatusEntry) IsUnstaged() bool {
	return e.Worktree != ' ' || e.IsUntracked()
}

func (e StatusEntry) IsTrackedUnstaged() bool {
	return !e.IsUntracked() && e.Worktree != ' '
}

func (e StatusEntry) IsUntracked() bool {
	return e.Index == '?' && e.Worktree == '?'
}

func (e *CommandError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git %s failed: %s", e.ArgString(), e.Stderr)
	}
	if e.ExitCode > 0 {
		return fmt.Sprintf("git %s failed with exit code %d", e.ArgString(), e.ExitCode)
	}
	if e.Err != nil {
		return fmt.Sprintf("git %s failed: %v", e.ArgString(), e.Err)
	}
	return fmt.Sprintf("git %s failed", e.ArgString())
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func (e *CommandError) ArgString() string {
	return strings.Join(e.Args, " ")
}

func (r Repository) run(ctx context.Context, args ...string) (string, error) {
	binary := r.Binary
	if binary == "" {
		binary = "git"
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		// stdout is returned alongside the error: some git commands write
		// useful context there even when they exit nonzero.
		return stdout.String(), &CommandError{
			Args:     args,
			ExitCode: exitCode,
			Stdout:   strings.TrimSpace(stdout.String()),
			Stderr:   strings.TrimSpace(stderr.String()),
			Err:      err,
		}
	}

	return stdout.String(), nil
}

// parseStatus reads `git status --porcelain=v1 -z` output. The -z form emits
// NUL-terminated records with raw, unquoted paths, so paths containing spaces,
// " -> ", or non-ASCII bytes survive intact. Rename and copy records carry the
// original path as a second NUL-separated field, ordered new-path-first.
func parseStatus(output string) Status {
	var entries []StatusEntry
	fields := strings.Split(output, "\x00")

	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			continue
		}

		entry := StatusEntry{
			Index:    field[0],
			Worktree: field[1],
			Path:     field[3:],
		}

		if entry.isRenameOrCopy() && i+1 < len(fields) {
			i++
			entry.OrigPath = fields[i]
		}

		entries = append(entries, entry)
	}
	return Status{Entries: entries}
}

func (e StatusEntry) isRenameOrCopy() bool {
	return e.Index == 'R' || e.Index == 'C' || e.Worktree == 'R' || e.Worktree == 'C'
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var result []string
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
