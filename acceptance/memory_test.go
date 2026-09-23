package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

// The fixture a pinned repository resolves to. Deliberately not the shape of
// this project's own memory tree: the parser must work on the documented
// format, not on one specific file.
func projectFiles() map[string]string {
	return map[string]string{
		"todo.md": `# Todo

## Active

### widget work → [plan](plans/widget.md)
- [x] Phase 1 — groundwork
- [~] Phase 2 — build the widget
  - [ ] W2.1 — sub-item with no link of its own
`,
		"plans/widget.md": `---
plan: widget
---

# Plan — widget

## Goal
Build the widget so the sprocket has something to turn.

## Success criteria
- it turns
`,
		"memory.md": `# Project: fixture

## Current State
Some state that must not be injected.

## Current Goal
Ship the widget by Friday.
`,
	}
}

func pinnedRepo(t *testing.T) (repo string, memRoot string) {
	t.Helper()

	repo = newRepo(t)
	memRoot = memoryTree(t, "fixture", projectFiles())
	pinRepo(t, repo, "fixture\n")

	writeFile(t, repo, "widget.go", "package widget\n")
	mustGit(t, repo, "add", "widget.go")
	return repo, memRoot
}

func memEnv(root string) []string { return []string{"AI_MEMORY_ROOT=" + root} }

// SC-17a — a pinned repo injects the active task, its plan's goal, and the
// project's current goal.
func TestSC17aPinnedRepoInjectsTaskPlanAndGoal(t *testing.T) {
	repo, memRoot := pinnedRepo(t)

	req := decodeGenRequest(t, runWith(t, repo, memEnv(memRoot), "commit", "--context-only"))

	task, ok := req.block("active task")
	if !ok {
		t.Fatalf("no active task block; labels = %v", req.contextLabels())
	}
	if !strings.Contains(task, "Phase 2 — build the widget") {
		t.Errorf("active task = %q, want the [~] item", task)
	}

	goal, ok := req.block("plan goal")
	if !ok {
		t.Fatalf("no plan goal block; labels = %v", req.contextLabels())
	}
	if !strings.Contains(goal, "something to turn") {
		t.Errorf("plan goal = %q", goal)
	}
	if strings.Contains(goal, "it turns") {
		t.Errorf("plan goal leaked the Success criteria section: %q", goal)
	}

	project, ok := req.block("project goal")
	if !ok {
		t.Fatalf("no project goal block; labels = %v", req.contextLabels())
	}
	if !strings.Contains(project, "Ship the widget") {
		t.Errorf("project goal = %q", project)
	}
	if strings.Contains(project, "must not be injected") {
		t.Errorf("project goal leaked the Current State section: %q", project)
	}
}

// SC-17a — the command's own blocks stay first and keep their existing shape,
// so the injection is additive rather than a reordering.
func TestSC17aMemoryBlocksAreAppendedNotSubstituted(t *testing.T) {
	repo, memRoot := pinnedRepo(t)

	labels := decodeGenRequest(t, runWith(t, repo, memEnv(memRoot), "commit", "--context-only")).contextLabels()

	want := []string{"staged files", "branch", "active task", "plan goal", "project goal"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("context labels = %v, want %v", labels, want)
	}
}

// SC-17b — an unpinned repo behaves exactly as it did before this feature:
// diff-only context, exit 0, nothing on stderr.
func TestSC17bUnpinnedRepoDegradesToDiffOnly(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "widget.go", "package widget\n")
	mustGit(t, repo, "add", "widget.go")

	r := run(t, repo, "commit", "--context-only")
	if r.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", r.exitCode, r.stderr)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty — being unpinned is ordinary, not a warning", r.stderr)
	}

	req := decodeGenRequest(t, r)
	if req.Diff == "" {
		t.Error("diff is empty; the request must still carry the change")
	}
	for _, label := range req.contextLabels() {
		if label == "active task" || label == "plan goal" || label == "project goal" {
			t.Errorf("unpinned repo emitted a memory block: %v", req.contextLabels())
		}
	}
}

// SC-17c — a marker that resolves to nothing degrades rather than failing.
func TestSC17cUnresolvableProjectDegrades(t *testing.T) {
	cases := []struct {
		name    string
		marker  string
		withEnv func(root string) []string
	}{
		{
			name:    "project that does not exist",
			marker:  "no-such-project\n",
			withEnv: memEnv,
		},
		{
			name:    "memory root that does not exist",
			marker:  "fixture\n",
			withEnv: func(string) []string { return []string{"AI_MEMORY_ROOT=/nonexistent-memory-root"} },
		},
		{
			name:    "empty marker",
			marker:  "\n",
			withEnv: memEnv,
		},
		{
			name:    "whitespace-only marker",
			marker:  "   \t \n",
			withEnv: memEnv,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, memRoot := pinnedRepo(t)
			pinRepo(t, repo, tc.marker)

			r := runWith(t, repo, tc.withEnv(memRoot), "commit", "--context-only")
			if r.exitCode != 0 {
				t.Fatalf("exit = %d, stderr = %q", r.exitCode, r.stderr)
			}

			req := decodeGenRequest(t, r)
			for _, label := range req.contextLabels() {
				if label == "active task" {
					t.Errorf("emitted memory context for an unresolvable project: %v", req.contextLabels())
				}
			}
		})
	}
}

// SC-17d — a marker naming a path must not reach outside the memory root.
//
// Decoys are planted at the paths a traversal actually lands on, computed the
// same way the code joins them: filepath.Join(base, "projects", marker). An
// earlier version of this test planted one decoy at <parent>/projects/fixture
// and passed even with the separator check removed, because "../../fixture"
// resolves to <parent>/fixture — it was asserting against a location no
// traversal reaches. Each decoy is proven readable before the assertion, so a
// pass means the check stopped the read rather than the path being empty.
func TestSC17dTraversalMarkerReadsNothingOutsideTheRoot(t *testing.T) {
	markers := []string{"../fixture", "../../fixture", "sub/fixture", "/etc/fixture", "./fixture", ".hidden"}

	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			repo, memRoot := pinnedRepo(t)

			landing := filepath.Join(memRoot, "projects", marker)
			if !strings.HasPrefix(landing, filepath.Join(memRoot, "projects")+string(filepath.Separator)) {
				plantDecoy(t, landing)
				// Proving the decoy loads when pointed at legitimately is what
				// stops this test from passing vacuously: the decoy sits at
				// <landing>, so a root of <landing>/../.. with project name
				// <base(landing)> would reach it.
				assertDecoyReadable(t, repo, landing)
			}

			pinRepo(t, repo, marker+"\n")
			r := runWith(t, repo, memEnv(memRoot), "commit", "--context-only")
			if r.exitCode != 0 {
				t.Fatalf("exit = %d, stderr = %q", r.exitCode, r.stderr)
			}
			if strings.Contains(r.stdout, "DECOY") {
				t.Errorf("marker %q escaped the memory root: %q", marker, r.stdout)
			}
		})
	}
}

// plantDecoy writes a complete, loadable project at dir, with every value
// marked DECOY so any read of it is unmistakable in the output.
func plantDecoy(t *testing.T, dir string) {
	t.Helper()
	for name, content := range projectFiles() {
		writeFileAt(t, filepath.Join(dir, name), strings.ReplaceAll(content, "widget", "DECOY"))
	}
}

// assertDecoyReadable proves a planted decoy is genuinely loadable, by pointing
// the tool at it through a legitimate root+name pair.
func assertDecoyReadable(t *testing.T, repo string, landing string) {
	t.Helper()

	shadow := t.TempDir()
	writeFileAt(t, filepath.Join(shadow, "projects", "decoyproj", "todo.md"), "- [~] DECOY task\n")
	pinRepo(t, repo, "decoyproj\n")
	if r := runWith(t, repo, memEnv(shadow), "commit", "--context-only"); !strings.Contains(r.stdout, "DECOY") {
		t.Fatalf("a legitimately-pinned project is not readable at all; this test proves nothing")
	}
}

// SC-17e — an oversized source is dropped whole, never emitted half-written.
func TestSC17eOversizedBlockIsDroppedNotTruncated(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "widget.go", "package widget\n")
	mustGit(t, repo, "add", "widget.go")

	files := projectFiles()
	// One enormous unbroken line: there is no line boundary to cut at, so the
	// block cannot be emitted partially.
	files["memory.md"] = "# Project\n\n## Current Goal\n" + strings.Repeat("x", 9000) + "\n"
	memRoot := memoryTree(t, "fixture", files)
	pinRepo(t, repo, "fixture\n")

	req := decodeGenRequest(t, runWith(t, repo, memEnv(memRoot), "commit", "--context-only"))

	if content, ok := req.block("project goal"); ok {
		t.Errorf("oversized project goal was emitted (%d bytes) instead of dropped", len(content))
	}
	// The higher-priority blocks must survive: dropping is lowest-first, not
	// all-or-nothing.
	if _, ok := req.block("active task"); !ok {
		t.Errorf("dropping the oversized block took the active task with it: %v", req.contextLabels())
	}
}

// SC-17e — every emitted block is whole. Asserted against the source rather
// than against a byte count, so a truncation that happened to land on a
// boundary would still fail.
func TestSC17eEmittedBlocksAreWhole(t *testing.T) {
	repo, memRoot := pinnedRepo(t)

	req := decodeGenRequest(t, runWith(t, repo, memEnv(memRoot), "commit", "--context-only"))

	goal, ok := req.block("plan goal")
	if !ok {
		t.Fatal("no plan goal block")
	}
	if goal != "Build the widget so the sprocket has something to turn." {
		t.Errorf("plan goal is not the whole section: %q", goal)
	}
}

// SC-17e — the per-block cap is load-bearing on its own, not just a
// consequence of the total budget. The block here is the only one and is sized
// to sit above the per-block cap but below the total budget, so the budget loop
// cannot account for it: only the per-block rule can drop it. Without this
// case, removing the per-block cap entirely leaves every other SC-17e
// assertion green.
func TestSC17ePerBlockCapActsIndependentlyOfTheBudget(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, repo, "widget.go", "package widget\n")
	mustGit(t, repo, "add", "widget.go")

	oversized := strings.Repeat("y", 3000) // > MaxBlockBytes (2048), < MaxContextBytes (4096)
	memRoot := memoryTree(t, "fixture", map[string]string{
		"todo.md": "- [~] " + oversized + "\n",
	})
	pinRepo(t, repo, "fixture\n")

	req := decodeGenRequest(t, runWith(t, repo, memEnv(memRoot), "commit", "--context-only"))
	if content, ok := req.block("active task"); ok {
		t.Errorf("a %d-byte single-line block survived the per-block cap (emitted %d bytes)", len(oversized), len(content))
	}
}

// SC-17f — a pinned repo does not surface its task or plan in the PR body, even
// though both are loaded. The memory context is input to describing the diff,
// not output a reviewer has to read past: the active task is whatever the author
// is working towards, which is routinely unrelated to the diff under review, and
// a step-by-step plan is a process artifact that already lives in the memory
// tree.
func TestSC17fPRBodyOmitsTaskAndPlan(t *testing.T) {
	repo, memRoot := pinnedRepo(t)
	mustGit(t, repo, "commit", "-qm", "feat: add the widget")
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	bodyPath := writeFakeGHCapturingBody(t, repo, prURL)

	r := runWith(t, repo, memEnv(memRoot), "pr")
	if r.exitCode != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", r.exitCode, r.stdout, r.stderr)
	}

	body := postedBody(t, bodyPath)
	for _, gone := range []string{"## Task", "## Plan", "Phase 2 — build the widget"} {
		if strings.Contains(body, gone) {
			t.Errorf("PR body surfaces %q from project memory:\n%s", gone, body)
		}
	}
}

// SC-17f — {{task}} and {{plan}} left the default template, not the renderer. A
// repository that wants them in its pull requests asks for them by name, and the
// memory context still supplies the values.
func TestSC17fCustomTemplateStillCarriesTaskAndPlan(t *testing.T) {
	repo, memRoot := pinnedRepo(t)
	mustGit(t, repo, "commit", "-qm", "feat: add the widget")
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	bodyPath := writeFakeGHCapturingBody(t, repo, prURL)

	writeFileAt(t, filepath.Join(repo, "pr-template.md"), "## Summary\n{{summary}}\n\n## Task\n{{task}}\n\n## Plan\n{{plan}}\n")
	writeRepoConfig(t, repo, "[pr]\ntemplate = \"pr-template.md\"\n")

	r := runWith(t, repo, memEnv(memRoot), "pr")
	if r.exitCode != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", r.exitCode, r.stdout, r.stderr)
	}

	body := postedBody(t, bodyPath)
	for _, kept := range []string{"## Task", "Phase 2 — build the widget", "## Plan", "widget"} {
		if !strings.Contains(body, kept) {
			t.Errorf("a template that asks for %q did not get it:\n%s", kept, body)
		}
	}
}

// SC-17g — with no memory project the Task and Plan sections are dropped
// entirely rather than posted as empty headings.
func TestSC17gUnpinnedPRBodyDropsEmptySections(t *testing.T) {
	repo := newRepo(t)
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	bodyPath := writeFakeGHCapturingBody(t, repo, prURL)

	r := run(t, repo, "pr")
	if r.exitCode != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", r.exitCode, r.stdout, r.stderr)
	}

	body := postedBody(t, bodyPath)
	// Task and Plan are absent from the default template; Prerequisites and
	// Ordering are in it but have no local source, so the offline path must drop
	// them the same way rather than posting the headings bare.
	for _, hollow := range []string{"## Task", "## Plan", "## Prerequisites", "## Ordering", "{{"} {
		if strings.Contains(body, hollow) {
			t.Errorf("posted %q with no value behind it:\n%s", hollow, body)
		}
	}
	if !strings.Contains(body, "## Description") {
		t.Errorf("dropped the sections that did have content:\n%s", body)
	}
}

// The never-merge invariant, re-asserted on the paths this phase touches.
func TestSC24MemoryPathsNeverMerge(t *testing.T) {
	repo, memRoot := pinnedRepo(t)
	mustGit(t, repo, "commit", "-qm", "feat: add the widget")
	withRemote(t, repo)
	onFeatureBranch(t, repo)
	log := writeFakeGH(t, repo, ghCreateSucceeds)

	runWith(t, repo, memEnv(memRoot), "pr")

	for _, call := range ghCalls(t, log) {
		if fields := strings.Fields(call); len(fields) >= 2 && fields[0] == "pr" && fields[1] == "merge" {
			t.Fatalf("gh pr merge was invoked: %q", call)
		}
	}
}
