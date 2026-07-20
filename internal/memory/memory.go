// Package memory resolves the memory project a repository is pinned to and
// loads the small slice of it worth putting in front of a generator: the active
// task, its plan's goal, and the project's current goal.
//
// Most repositories are not pinned. Every failure here — no marker, no memory
// tree, a marker naming a project that does not exist — resolves to "no memory
// project" rather than an error, because being unpinned is the ordinary case.
package memory

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/seyio91/git-cli-ai/internal/ai"
)

// MarkerPath is the repo-relative file naming the memory project, written by
// the memory system's own pin command.
const MarkerPath = ".agents/memory-project"

// Block labels, in priority order.
const (
	LabelTask        = "active task"
	LabelPlanGoal    = "plan goal"
	LabelProjectGoal = "project goal"
)

// Byte ceilings for the assembled context. MaxBlockBytes is generous enough
// that the sections these blocks are cut from — a todo line, a plan's Goal, a
// project's Current Goal — fit whole in practice, so the cap is a guard against
// a pathological file rather than a routine trim. MaxContextBytes is twice
// that: two full-sized blocks survive, and a third only if the set is
// realistically sized.
const (
	MaxBlockBytes   = 2048
	MaxContextBytes = 4096
)

// Project is a resolved memory project directory.
type Project struct {
	Name string
	Dir  string
}

// Context is what a pinned repository contributes to a generation prompt.
// Blocks is ordered highest priority first. Task and Plan carry the same
// values in the raw form the PR template's {{task}} and {{plan}} want.
type Context struct {
	Blocks []ai.ContextBlock
	Task   string
	Plan   string
}

// Discover resolves the memory project repoRoot is pinned to. The second
// return is false whenever there is no usable project, which is not an error.
func Discover(repoRoot string) (Project, bool) {
	if repoRoot == "" {
		return Project{}, false
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, MarkerPath))
	if err != nil {
		return Project{}, false
	}

	// The marker is repo-controlled and feeds a filesystem path, so only a
	// bare project name is accepted: no separator, no traversal, no dotfile.
	name := strings.TrimSpace(string(data))
	if !validName(name) {
		return Project{}, false
	}

	base, ok := baseDir()
	if !ok {
		return Project{}, false
	}

	// Resolve through the filesystem before trusting the path: a lexical check
	// alone would accept a projects/<name> entry that is itself a symlink out
	// of the tree.
	dir, ok := resolveWithin(base, filepath.Join(base, "projects", name))
	if !ok {
		return Project{}, false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Project{}, false
	}

	return Project{Name: name, Dir: dir}, true
}

// Load reads the project's context. Sources that are missing, unreadable or
// empty contribute nothing; there is no failure mode worth reporting to a
// caller who will degrade to diff-only context either way.
func Load(p Project) Context {
	ctx := Context{}
	if p.Dir == "" {
		return ctx
	}

	var blocks []ai.ContextBlock

	ctx.Task, ctx.Plan = activeTodo(p.Dir)
	if ctx.Task != "" {
		blocks = append(blocks, ai.ContextBlock{Label: LabelTask, Content: ctx.Task})
	}
	if ctx.Plan != "" {
		if goal := section(p.Dir, filepath.Join(p.Dir, "plans", ctx.Plan+".md"), "goal"); goal != "" {
			blocks = append(blocks, ai.ContextBlock{Label: LabelPlanGoal, Content: goal})
		}
	}
	if goal := section(p.Dir, filepath.Join(p.Dir, "memory.md"), "current goal"); goal != "" {
		blocks = append(blocks, ai.ContextBlock{Label: LabelProjectGoal, Content: goal})
	}

	ctx.Blocks = fit(blocks)
	return ctx
}

// baseDir resolves the memory tree root. Symlinks are followed deliberately:
// the default location is commonly a link into a checked-out tree, and refusing
// to follow it would make the zero-config case fail.
func baseDir() (string, bool) {
	base := strings.TrimSpace(os.Getenv("AI_MEMORY_ROOT"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("MEMORY_DIR"))
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		base = filepath.Join(home, ".claude-memory")
	}

	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", false
	}
	return resolved, true
}

func validName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return false
	}
	return name == filepath.Base(name)
}

func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveWithin resolves path through any symlinks and confirms the result is
// still under base. Every path this package reads goes through it, so a
// symlinked entry inside the memory tree cannot widen what is readable.
func resolveWithin(base, path string) (string, bool) {
	// Both sides must be resolved before comparing: on macOS even a plain temp
	// dir is reached through a symlink, so comparing a resolved path against an
	// unresolved base rejects paths that are genuinely inside it.
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	if !within(resolvedBase, resolved) {
		return "", false
	}
	return resolved, true
}

// readWithin reads a file only if it resolves to somewhere under dir.
func readWithin(dir, path string) ([]byte, bool) {
	resolved, ok := resolveWithin(dir, path)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, false
	}
	return data, true
}

// activeTodo returns the active todo item and the plan it references. The
// active item is the one marked [~]; failing that, the first unchecked one. A
// plan link on the item's own line wins, else the nearest one above it, which
// is how a nested sub-item inherits its group's plan.
func activeTodo(dir string) (task string, plan string) {
	data, ok := readWithin(dir, filepath.Join(dir, "todo.md"))
	if !ok {
		return "", ""
	}

	var firstTask, firstPlan string
	var seenFirst bool

	// Plan links are scoped by indentation: an item's link covers the item and
	// anything nested under it, but not a sibling that follows. A frame at
	// indent -1 comes from a heading and covers everything below it.
	type frame struct {
		indent int
		plan   string
	}
	var scope []frame

	for _, line := range strings.Split(string(data), "\n") {
		text, state, indent, isItem := todoItem(line)
		ref := planRef(line)

		if !isItem {
			if ref != "" {
				scope = []frame{{indent: -1, plan: ref}}
			}
			continue
		}

		for len(scope) > 0 && scope[len(scope)-1].indent >= indent {
			scope = scope[:len(scope)-1]
		}
		itemPlan := ""
		if len(scope) > 0 {
			itemPlan = scope[len(scope)-1].plan
		}
		if ref != "" {
			itemPlan = ref
			scope = append(scope, frame{indent: indent, plan: ref})
		}

		switch state {
		case '~':
			return text, itemPlan
		case ' ':
			if !seenFirst {
				firstTask, firstPlan, seenFirst = text, itemPlan, true
			}
		}
	}
	return firstTask, firstPlan
}

// todoItem splits a markdown checkbox line into its state character, text and
// indentation depth.
func todoItem(line string) (text string, state byte, indent int, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	indent = len(line) - len(trimmed)
	if !strings.HasPrefix(trimmed, "- [") && !strings.HasPrefix(trimmed, "* [") {
		return "", 0, 0, false
	}
	rest := trimmed[3:]
	if len(rest) < 2 || rest[1] != ']' {
		return "", 0, 0, false
	}
	text = strings.TrimSpace(rest[2:])
	if text == "" {
		return "", 0, 0, false
	}
	return text, rest[0], indent, true
}

func planRef(line string) string {
	_, after, ok := strings.Cut(line, "](plans/")
	if !ok {
		return ""
	}
	name, _, ok := strings.Cut(after, ".md)")
	if !ok || !validName(name) {
		return ""
	}
	return name
}

// section returns the body of the markdown section with the given normalised
// heading, up to the next heading. Headings inside fenced code or HTML comments
// are ignored. The file is read only if it resolves to somewhere under dir.
func section(dir, path, heading string) string {
	data, ok := readWithin(dir, path)
	if !ok {
		return ""
	}
	return sectionFrom(string(data), heading)
}

func sectionFrom(text, heading string) string {
	lines := strings.Split(text, "\n")

	// Unbalanced markers mean the file is malformed, not that everything after
	// them is code or comment. Honouring them would let one stray ``` or <!--
	// swallow the rest of the file — dragging unrelated sections into this one,
	// or erasing a heading that genuinely follows. Treat them as literal text.
	fences := 0
	for _, raw := range lines {
		if strings.HasPrefix(strings.TrimSpace(raw), "```") {
			fences++
		}
	}
	useFences := fences%2 == 0
	useComments := strings.Count(text, "<!--") == strings.Count(text, "-->")

	var body []string
	inSection, fenced, commented := false, false, false

	for _, raw := range lines {
		if useComments {
			stripped, stillOpen := stripComments(raw, commented)
			wasCommented := commented
			commented = stillOpen
			if stripped != raw {
				if strings.TrimSpace(stripped) == "" && (wasCommented || strings.TrimSpace(raw) != "") {
					continue
				}
				raw = strings.TrimRight(stripped, " \t")
			}
		}

		line := strings.TrimSpace(raw)
		if useFences && strings.HasPrefix(line, "```") {
			fenced = !fenced
		} else if !fenced && strings.HasPrefix(line, "#") {
			if inSection {
				break
			}
			inSection = normaliseHeading(line) == heading
			continue
		}
		if inSection {
			body = append(body, raw)
		}
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}

// stripComments removes HTML comment spans from a line, carrying open-comment
// state across lines. Handles an opener that starts mid-line, which a
// prefix-only check misses.
func stripComments(line string, commented bool) (string, bool) {
	var out strings.Builder
	for {
		if commented {
			idx := strings.Index(line, "-->")
			if idx < 0 {
				return out.String(), true
			}
			line = line[idx+len("-->"):]
			commented = false
			continue
		}
		idx := strings.Index(line, "<!--")
		if idx < 0 {
			out.WriteString(line)
			return out.String(), false
		}
		out.WriteString(line[:idx])
		line = line[idx+len("<!--"):]
		commented = true
	}
}

func normaliseHeading(line string) string {
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	return strings.ToLower(strings.Join(strings.Fields(line), " "))
}

// fit applies the per-block cap and then drops whole blocks, lowest priority
// first, until the total fits. A block is never emitted truncated mid-content:
// a half-written fact reads as a complete one to a model.
func fit(blocks []ai.ContextBlock) []ai.ContextBlock {
	kept := make([]ai.ContextBlock, 0, len(blocks))
	for _, block := range blocks {
		content, ok := capBlock(block.Content)
		if !ok {
			continue
		}
		block.Content = content
		kept = append(kept, block)
	}

	for len(kept) > 0 && totalBytes(kept) > MaxContextBytes {
		kept = kept[:len(kept)-1]
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// capBlock trims content to the last line break within the cap. A first line
// that alone exceeds the cap has no clean boundary, so the block is dropped.
func capBlock(content string) (string, bool) {
	if len(content) <= MaxBlockBytes {
		return content, true
	}
	cut := strings.LastIndexByte(content[:MaxBlockBytes+1], '\n')
	if cut <= 0 {
		return "", false
	}
	trimmed := strings.TrimRight(content[:cut], "\n")
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func totalBytes(blocks []ai.ContextBlock) int {
	total := 0
	for _, block := range blocks {
		total += len(block.Content)
	}
	return total
}
