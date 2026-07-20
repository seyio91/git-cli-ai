package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const todoFixture = `# Todo — demo

> Large items link to a plan under ` + "`plans/`" + `.

## Active

### demo v1 → [plan](plans/demo-v1.md)
- [x] Phase 1 — done → [plan](plans/done.md)
- [~] Phase 2 — memory awareness → [plan](plans/phase2.md)
  - [ ] P2.1 — the package
- [ ] Phase 3 — later
`

const planFixture = `---
plan: phase2
---

# Plan — Phase 2

## Goal

Make the tool aware of the task it is committing against.

## Success criteria

- SC-1 — something.
`

const memoryFixture = `# Memory — demo

## Current State

Phase 1 shipped.

## Current Goal

Ship phase 2 behind one package.

## Related Projects

None.
`

// writeTree materialises files under root; keys are slash-separated relative
// paths.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// newTree builds a memory root holding the demo project, points the resolver at
// it, and returns the root.
func newTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"projects/demo/todo.md":          todoFixture,
		"projects/demo/plans/phase2.md":  planFixture,
		"projects/demo/memory.md":        memoryFixture,
		"projects/demo/working.md":       "## Checkpoints\n\nSECRET-WORKING-MEMORY\n",
		"projects/demo/plans/done.md":    "## Goal\n\nThe wrong plan.\n",
		"projects/demo/plans/demo-v1.md": "## Goal\n\nThe parent plan.\n",
		"projects/other/todo.md":         "- [ ] wrong project\n",
	})
	t.Setenv("AI_MEMORY_ROOT", root)
	t.Setenv("MEMORY_DIR", "")
	return root
}

// pin writes the marker into a fresh repo root and returns it.
func pin(t *testing.T, name string) string {
	t.Helper()
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{MarkerPath: name})
	return repo
}

func labels(ctx Context) []string {
	out := make([]string, 0, len(ctx.Blocks))
	for _, block := range ctx.Blocks {
		out = append(out, block.Label)
	}
	return out
}

func content(t *testing.T, ctx Context, label string) string {
	t.Helper()
	for _, block := range ctx.Blocks {
		if block.Label == label {
			return block.Content
		}
	}
	t.Fatalf("no %q block in %v", label, labels(ctx))
	return ""
}

// A pinned repo yields all three blocks, in priority order, from the right
// sources: the [~] item, the plan that item links, and Current Goal only.
func TestDiscoverAndLoadPinnedProject(t *testing.T) {
	newTree(t)
	repo := pin(t, "demo\n")

	project, ok := Discover(repo)
	if !ok {
		t.Fatal("a pinned repo resolved to no memory project")
	}
	if project.Name != "demo" {
		t.Fatalf("project name = %q, want demo", project.Name)
	}

	ctx := Load(project)
	want := []string{LabelTask, LabelPlanGoal, LabelProjectGoal}
	if got := labels(ctx); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("blocks = %v, want %v in priority order", got, want)
	}

	if !strings.Contains(content(t, ctx, LabelTask), "Phase 2 — memory awareness") {
		t.Fatalf("active task block = %q", content(t, ctx, LabelTask))
	}
	if goal := content(t, ctx, LabelPlanGoal); !strings.Contains(goal, "aware of the task") || strings.Contains(goal, "SC-1") {
		t.Fatalf("plan goal block carried the wrong section: %q", goal)
	}
	if goal := content(t, ctx, LabelProjectGoal); !strings.Contains(goal, "Ship phase 2") || strings.Contains(goal, "Phase 1 shipped") {
		t.Fatalf("project goal block carried the wrong section: %q", goal)
	}

	if ctx.Plan != "phase2" {
		t.Fatalf("Plan = %q, want phase2", ctx.Plan)
	}
	if !strings.Contains(ctx.Task, "Phase 2") {
		t.Fatalf("Task = %q", ctx.Task)
	}

	for _, block := range ctx.Blocks {
		if strings.Contains(block.Content, "SECRET-WORKING-MEMORY") {
			t.Fatal("working.md leaked into the context; it is deliberately not read")
		}
	}
}

// Unpinned is the ordinary case, not a failure.
func TestDiscoverUnpinnedRepo(t *testing.T) {
	newTree(t)
	if _, ok := Discover(t.TempDir()); ok {
		t.Fatal("a repo with no marker resolved to a memory project")
	}
}

func TestDiscoverDegradesWithoutError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		marker  string
		prepare func(t *testing.T, root string)
	}{
		{name: "project does not exist", marker: "missing"},
		{
			name:   "memory root does not exist",
			marker: "demo",
			prepare: func(t *testing.T, _ string) {
				t.Setenv("AI_MEMORY_ROOT", filepath.Join(t.TempDir(), "absent"))
			},
		},
		{
			name:   "project path is a file",
			marker: "afile",
			prepare: func(t *testing.T, root string) {
				writeTree(t, root, map[string]string{"projects/afile": "x"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newTree(t)
			if tc.prepare != nil {
				tc.prepare(t, root)
			}
			if _, ok := Discover(pin(t, tc.marker)); ok {
				t.Fatalf("%s resolved to a memory project", tc.name)
			}
		})
	}
}

// The marker is repo-controlled and feeds a filesystem path. Rejection is
// asserted on the filesystem too: the decoy outside the memory root must stay
// unread.
func TestDiscoverRejectsNonBareNames(t *testing.T) {
	for _, name := range []string{
		"", " ", "\n\t ", ".", "..", "../other", "../../etc/passwd",
		"demo/../other", "sub/demo", `sub\demo`, "/etc/passwd",
		`C:\windows`, ".hidden", "./demo",
	} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			root := newTree(t)

			outside := filepath.Dir(root)
			writeTree(t, outside, map[string]string{"projects/other/todo.md": "- [ ] DECOY-OUTSIDE-ROOT\n"})

			project, ok := Discover(pin(t, name))
			if ok {
				t.Fatalf("marker %q resolved to %q; a non-bare name must be rejected", name, project.Dir)
			}
			if project.Dir != "" && !within(root, project.Dir) {
				t.Fatalf("resolved dir %q escaped the memory root %q", project.Dir, root)
			}
			if ctx := Load(project); len(ctx.Blocks) != 0 {
				t.Fatalf("a rejected marker still produced context: %v", ctx.Blocks)
			}
		})
	}
}

// The rejection above is only worth as much as the escape it prevents. Here
// each traversal actually lands on a readable project, so a missing check is
// caught by the read succeeding rather than by the path not existing.
func TestDiscoverDoesNotEscapeToAReachableProject(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "inner")

	// Reachable from <root>/projects/<name> as ../../decoy and ../sibling.
	writeTree(t, parent, map[string]string{"decoy/todo.md": "- [ ] DECOY-OUTSIDE-ROOT\n"})
	writeTree(t, root, map[string]string{
		"sibling/todo.md":       "- [ ] DECOY-OUTSIDE-PROJECTS\n",
		"projects/demo/todo.md": todoFixture,
	})
	t.Setenv("AI_MEMORY_ROOT", root)

	for _, name := range []string{"../../decoy", "../sibling", "../../decoy/"} {
		t.Run(name, func(t *testing.T) {
			// The target is genuinely there: the check, not the filesystem,
			// has to be what stops it.
			target := filepath.Join(root, "projects", name)
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("test setup: %s is not reachable: %v", target, err)
			}

			project, ok := Discover(pin(t, name))
			if ok {
				t.Fatalf("marker %q resolved to %q", name, project.Dir)
			}
			if ctx := Load(project); ctx.Task != "" {
				t.Fatalf("read a project outside <root>/projects: %q", ctx.Task)
			}
		})
	}
}

func TestBasePathResolutionOrder(t *testing.T) {
	primary := t.TempDir()
	writeTree(t, primary, map[string]string{"projects/demo/todo.md": "- [ ] from AI_MEMORY_ROOT\n"})
	fallback := t.TempDir()
	writeTree(t, fallback, map[string]string{"projects/demo/todo.md": "- [ ] from MEMORY_DIR\n"})

	repo := pin(t, "demo")

	t.Run("AI_MEMORY_ROOT wins", func(t *testing.T) {
		t.Setenv("AI_MEMORY_ROOT", primary)
		t.Setenv("MEMORY_DIR", fallback)

		project, ok := Discover(repo)
		if !ok {
			t.Fatal("no project resolved")
		}
		if task := Load(project).Task; task != "from AI_MEMORY_ROOT" {
			t.Fatalf("task = %q, want the AI_MEMORY_ROOT tree", task)
		}
	})

	t.Run("MEMORY_DIR is the fallback", func(t *testing.T) {
		t.Setenv("AI_MEMORY_ROOT", "")
		t.Setenv("MEMORY_DIR", fallback)

		project, ok := Discover(repo)
		if !ok {
			t.Fatal("no project resolved")
		}
		if task := Load(project).Task; task != "from MEMORY_DIR" {
			t.Fatalf("task = %q, want the MEMORY_DIR tree", task)
		}
	})
}

// The default location is commonly a symlink into a checked-out tree, so
// following links is required for the zero-config case to work.
func TestBasePathFollowsSymlinks(t *testing.T) {
	real := t.TempDir()
	writeTree(t, real, map[string]string{"projects/demo/todo.md": "- [ ] behind a symlink\n"})

	link := filepath.Join(t.TempDir(), "memory-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("AI_MEMORY_ROOT", link)

	project, ok := Discover(pin(t, "demo"))
	if !ok {
		t.Fatal("a symlinked memory root resolved to no project")
	}
	if task := Load(project).Task; task != "behind a symlink" {
		t.Fatalf("task = %q", task)
	}
}

func TestActiveTodoSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		todo     string
		wantTask string
		wantPlan string
	}{
		{
			name:     "in-progress item beats the first unchecked one",
			todo:     "- [ ] first unchecked → [plan](plans/a.md)\n- [~] in progress → [plan](plans/b.md)\n",
			wantTask: "in progress → [plan](plans/b.md)",
			wantPlan: "b",
		},
		{
			name:     "first unchecked item when nothing is in progress",
			todo:     "- [x] done → [plan](plans/a.md)\n- [ ] next → [plan](plans/b.md)\n- [ ] later → [plan](plans/c.md)\n",
			wantTask: "next → [plan](plans/b.md)",
			wantPlan: "b",
		},
		{
			name:     "a nested sub-item inherits the plan above it",
			todo:     "### group → [plan](plans/group.md)\n- [x] done\n  - [~] sub-item\n",
			wantTask: "sub-item",
			wantPlan: "group",
		},
		{
			name: "nothing left to do",
			todo: "- [x] done\n- [x] also done\n",
		},
		{
			name:     "no plan link at all",
			todo:     "- [~] unplanned work\n",
			wantTask: "unplanned work",
		},
		{
			name:     "a traversal-shaped plan link is ignored",
			todo:     "- [~] work → [plan](plans/../../../etc/passwd.md)\n",
			wantTask: "work → [plan](plans/../../../etc/passwd.md)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, map[string]string{"projects/demo/todo.md": tc.todo})
			t.Setenv("AI_MEMORY_ROOT", root)

			project, ok := Discover(pin(t, "demo"))
			if !ok {
				t.Fatal("no project resolved")
			}

			ctx := Load(project)
			if ctx.Task != tc.wantTask {
				t.Fatalf("Task = %q, want %q", ctx.Task, tc.wantTask)
			}
			if ctx.Plan != tc.wantPlan {
				t.Fatalf("Plan = %q, want %q", ctx.Plan, tc.wantPlan)
			}
		})
	}
}

// Missing or empty sources drop out silently rather than emitting empty blocks.
func TestLoadOmitsMissingSources(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"projects/demo/memory.md": "## Current Goal\n\nOnly this.\n",
	})
	t.Setenv("AI_MEMORY_ROOT", root)

	project, ok := Discover(pin(t, "demo"))
	if !ok {
		t.Fatal("no project resolved")
	}

	ctx := Load(project)
	if got := labels(ctx); len(got) != 1 || got[0] != LabelProjectGoal {
		t.Fatalf("blocks = %v, want only the project goal", got)
	}
}

func TestLoadOnAnEmptyProjectYieldsNothing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_MEMORY_ROOT", root)

	project, ok := Discover(pin(t, "demo"))
	if !ok {
		t.Fatal("no project resolved")
	}
	if ctx := Load(project); len(ctx.Blocks) != 0 || ctx.Task != "" || ctx.Plan != "" {
		t.Fatalf("an empty project produced %#v", ctx)
	}
}

func TestLoadOnAnUnreadableProjectDirYieldsNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	root := t.TempDir()
	writeTree(t, root, map[string]string{"projects/demo/todo.md": todoFixture})
	dir := filepath.Join(root, "projects", "demo")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	t.Setenv("AI_MEMORY_ROOT", root)

	project, _ := Discover(pin(t, "demo"))
	if ctx := Load(project); len(ctx.Blocks) != 0 {
		t.Fatalf("an unreadable project produced %v", labels(ctx))
	}
}

// An oversized block is cut back to a line break, never mid-content.
func TestPerBlockCapCutsAtALineBoundary(t *testing.T) {
	line := strings.Repeat("x", 79) + "\n"
	oversized := strings.Repeat(line, (MaxBlockBytes/len(line))+4)

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"projects/demo/memory.md": "## Current Goal\n\n" + oversized + "\n## Next\n",
	})
	t.Setenv("AI_MEMORY_ROOT", root)

	project, _ := Discover(pin(t, "demo"))
	ctx := Load(project)

	got := content(t, ctx, LabelProjectGoal)
	if len(got) > MaxBlockBytes {
		t.Fatalf("block is %d bytes, over the %d cap", len(got), MaxBlockBytes)
	}
	if len(got) == 0 {
		t.Fatal("the block was dropped entirely; it had clean boundaries to cut at")
	}
	for _, l := range strings.Split(got, "\n") {
		if l != "" && len(l) != 79 {
			t.Fatalf("a line was cut mid-content: %q", l)
		}
	}
}

// A single line over the cap has no clean boundary, so the block goes rather
// than being half-written.
func TestPerBlockCapDropsAnUncuttableBlock(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"projects/demo/memory.md": "## Current Goal\n\n" + strings.Repeat("y", MaxBlockBytes+100) + "\n",
	})
	t.Setenv("AI_MEMORY_ROOT", root)

	project, _ := Discover(pin(t, "demo"))
	if ctx := Load(project); len(ctx.Blocks) != 0 {
		t.Fatalf("an uncuttable block was emitted: %d bytes", len(ctx.Blocks[0].Content))
	}
}

// Over budget, whole blocks go lowest priority first: project goal, then plan
// goal, and the active task last.
func TestBudgetDropsWholeBlocksLowestPriorityFirst(t *testing.T) {
	bulk := strings.Repeat(strings.Repeat("z", 63)+"\n", MaxBlockBytes/64)

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"projects/demo/todo.md":      "- [~] the active task → [plan](plans/big.md)\n",
		"projects/demo/plans/big.md": "## Goal\n\n" + bulk,
		"projects/demo/memory.md":    "## Current Goal\n\n" + bulk,
	})
	t.Setenv("AI_MEMORY_ROOT", root)

	project, _ := Discover(pin(t, "demo"))
	ctx := Load(project)

	if got := labels(ctx); len(got) != 2 || got[0] != LabelTask || got[1] != LabelPlanGoal {
		t.Fatalf("blocks = %v, want the project goal dropped and the rest intact", got)
	}
	if total := totalBytes(ctx.Blocks); total > MaxContextBytes {
		t.Fatalf("kept %d bytes, over the %d budget", total, MaxContextBytes)
	}
	if content(t, ctx, LabelPlanGoal) != strings.TrimSpace(bulk) {
		t.Fatal("a surviving block was truncated; only whole blocks may be dropped")
	}
	if ctx.Plan != "big" || ctx.Task == "" {
		t.Fatalf("dropping a block also dropped the template values: %#v", ctx)
	}
}
