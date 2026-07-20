// Package pr renders pull request bodies and opens pull requests through a
// forge provider. Nothing in this package can merge: the Provider interface has
// no merge method and the gh implementation builds no such argv.
package pr

import (
	"os"
	"strings"
)

// DefaultTemplate is the built-in body structure. Its heading lines are what a
// supplied body is checked against, so the structure and the conformance rule
// cannot drift apart.
const DefaultTemplate = `## Summary
{{summary}}

## Changes
{{changes}}

## Task
{{task}}

## Plan
{{plan}}

## Testing
{{testing}}
`

// Template is the active PR body structure, either the built-in one or a file
// named by pr.template.
type Template struct {
	Text string
}

// LoadTemplate reads the template at path, falling back to the built-in one when
// no path is configured. A configured path that cannot be read is an error: the
// caller asked for a specific structure and silently substituting another would
// produce a body they did not ask for.
func LoadTemplate(path string) (Template, error) {
	if path == "" {
		return Template{Text: DefaultTemplate}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	return Template{Text: string(data)}, nil
}

// Headings returns the heading lines the template defines, normalised. Lines
// inside fenced code are skipped: a `# comment` in a shell snippet is not a
// section an author has to reproduce.
func (t Template) Headings() []string {
	var headings []string
	fenced := false

	for _, line := range strings.Split(t.Text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(line, "#") {
			continue
		}
		if h := normaliseHeading(line); h != "" {
			headings = append(headings, h)
		}
	}
	return headings
}

// normaliseHeading reduces a heading to the part that carries meaning: the text
// itself, lowercased. Level, spacing and trailing punctuation are cosmetic, and
// treating them as structural is not a free mistake — a near-miss sends a
// finished body to the generator, which replaces it.
func normaliseHeading(line string) string {
	line = strings.TrimLeft(line, "#")
	line = strings.TrimSpace(line)
	line = strings.TrimRight(line, ":：.-")
	return strings.ToLower(strings.Join(strings.Fields(line), " "))
}

// Fills reports whether body is a finished instance of this template: every
// section the template defines is present *and* carries content of its own.
//
// The content requirement is what stops the template itself from qualifying.
// Feeding the raw template back in — which is exactly what happens when someone
// points --body-file at the repo's PR template — matches every heading, and a
// heading-only check would post `{{summary}}` to the forge verbatim. A section
// whose only content is the placeholder it was meant to replace is not filled.
// Absence is not tolerated here, deliberately. A body that omits sections is
// treated as intent to be rendered, which is what lets someone pass a rough
// --body and get it shaped into the house format. That rule collides with
// wanting Render's own output to round-trip: with no memory project, Render
// drops Task, Plan and Testing, leaving exactly the Summary+Changes shape this
// check must reject. The rendering rule wins, because it is the one a human
// relies on; a rendered body fed back through --body-file is regenerated rather
// than reused. See the plan's SC-17g note.
func (t Template) Fills(body string) bool {
	headings := t.Headings()
	if len(headings) == 0 {
		// No structure to conform to; nothing can fail to match it.
		return true
	}

	sections := sectionContent(body)
	for _, heading := range headings {
		content, ok := sections[heading]
		if !ok || content == "" {
			return false
		}
	}
	return true
}

// sectionContent maps each normalised heading in body to the non-empty,
// non-placeholder text beneath it.
func sectionContent(body string) map[string]string {
	sections := make(map[string]string)
	current := ""
	fenced := false

	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(line, "#") {
			current = normaliseHeading(line)
			if _, seen := sections[current]; !seen {
				sections[current] = ""
			}
			continue
		}
		if current == "" || line == "" || isPlaceholder(line) {
			continue
		}
		sections[current] += line
	}
	return sections
}

// isPlaceholder reports whether a line is nothing but an unsubstituted
// {{placeholder}}.
func isPlaceholder(line string) bool {
	return strings.HasPrefix(line, "{{") && strings.HasSuffix(line, "}}") &&
		!strings.Contains(strings.TrimSuffix(strings.TrimPrefix(line, "{{"), "}}"), "{{")
}

// Render substitutes {{placeholder}} values and drops any section left with no
// content. {{task}} and {{plan}} are empty in every repository that is not
// pinned to a memory project, which is most of them; emitting `## Task` above
// nothing puts a hollow heading in front of every reviewer. Dropping the
// heading too also means the result satisfies Fills, so a rendered body fed
// back through --body-file is recognised as finished rather than regenerated.
//
// Section boundaries come from the template, not from the substituted text: a
// value that happens to begin with # is content, not a new heading.
func (t Template) Render(values map[string]string) string {
	var out, section []string
	inSection, hasContent, fenced := false, false, false

	flush := func() {
		if inSection && hasContent {
			out = append(out, section...)
		}
		section, hasContent = nil, false
	}

	for _, raw := range strings.Split(t.Text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
		}
		line := substitute(raw, values)

		if !fenced && strings.HasPrefix(trimmed, "#") {
			flush()
			inSection, section = true, []string{line}
			continue
		}
		if !inSection {
			out = append(out, line)
			continue
		}

		section = append(section, line)
		if strings.TrimSpace(line) != "" {
			hasContent = true
		}
	}
	flush()

	return strings.Join(out, "\n")
}

func substitute(line string, values map[string]string) string {
	for _, name := range placeholderNames(line) {
		line = strings.ReplaceAll(line, "{{"+name+"}}", values[name])
	}
	return line
}

func placeholderNames(text string) []string {
	var names []string
	rest := text
	for {
		_, after, ok := strings.Cut(rest, "{{")
		if !ok {
			return names
		}
		name, remainder, ok := strings.Cut(after, "}}")
		if !ok {
			return names
		}
		names = append(names, name)
		rest = remainder
	}
}
