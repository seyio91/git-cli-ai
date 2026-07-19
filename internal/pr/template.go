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

// Render substitutes {{placeholder}} values. Unfilled placeholders collapse to
// an empty line rather than surviving into the PR: {{task}} and {{plan}} are
// routinely empty outside a memory project.
func (t Template) Render(values map[string]string) string {
	rendered := t.Text
	for _, name := range placeholderNames(t.Text) {
		rendered = strings.ReplaceAll(rendered, "{{"+name+"}}", values[name])
	}
	return rendered
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
