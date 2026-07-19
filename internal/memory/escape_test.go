package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project entry that is itself a symlink out of the tree must not be read.
func TestDiscoverRejectsSymlinkedProjectDir(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "mem")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(filepath.Join(base, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "todo.md"), []byte("- [ ] SECRET OUTSIDE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "projects", "demo")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, MarkerPath), []byte("demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_MEMORY_ROOT", base)
	t.Setenv("MEMORY_DIR", "")

	p, ok := Discover(repo)
	if ok {
		c := Load(p)
		if strings.Contains(c.Task, "SECRET OUTSIDE") {
			t.Fatalf("read content from outside the memory root: dir=%q task=%q", p.Dir, c.Task)
		}
		t.Fatalf("discovered a symlinked-out project dir: %q", p.Dir)
	}
}

// A completed item's plan link must not leak into a later active item.
func TestActiveTodoDoesNotInheritFromCompletedSibling(t *testing.T) {
	dir := t.TempDir()
	body := "- [x] done → [plan](plans/done.md)\n- [~] in progress\n"
	if err := os.WriteFile(filepath.Join(dir, "todo.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	task, plan := activeTodo(dir)
	if task != "in progress" {
		t.Fatalf("task = %q", task)
	}
	if plan != "" {
		t.Errorf("plan = %q, want empty — a completed sibling's plan link leaked", plan)
	}
}

// An unterminated fence must not drag later sections into this one.
func TestSectionUnterminatedFenceDoesNotSwallowLaterSections(t *testing.T) {
	body := "## Current Goal\nreal goal\n\n```\nunclosed\n\n## Related Projects\nOTHER PROJECT INFO\n"
	if got := sectionFrom(body, "current goal"); strings.Contains(got, "OTHER PROJECT INFO") {
		t.Errorf("unterminated fence swallowed a later section:\n%s", got)
	}
}

// An unterminated comment before the heading must not erase the section.
func TestSectionUnterminatedCommentDoesNotEraseLaterHeading(t *testing.T) {
	body := "## Unrelated\n<!-- forgot to close\n## Current Goal\nreal goal\n"
	if got := sectionFrom(body, "current goal"); got != "real goal" {
		t.Errorf("section() = %q, want %q", got, "real goal")
	}
}

// A comment opened mid-line must still be stripped.
func TestSectionStripsMidLineComment(t *testing.T) {
	body := "## Current Goal\nreal goal <!-- reword this -->\ntail\n"
	got := sectionFrom(body, "current goal")
	if strings.Contains(got, "<!--") || strings.Contains(got, "reword") {
		t.Errorf("mid-line comment leaked: %q", got)
	}
	if !strings.Contains(got, "real goal") || !strings.Contains(got, "tail") {
		t.Errorf("stripping removed real content: %q", got)
	}
}
