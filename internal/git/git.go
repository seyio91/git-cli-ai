package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
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

func (r Repository) Push(ctx context.Context, remote string, branch string, setUpstream bool) error {
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
