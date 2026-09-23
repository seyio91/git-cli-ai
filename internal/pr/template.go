// Package pr renders pull request bodies and opens pull requests through a
// forge provider. Nothing in this package can merge: the Provider interface has
// no merge method and the gh implementation builds no such argv.
package pr

import (
	"os"
	"strings"
)

// DefaultTemplate is the built-in body structure, shaped for the person reading
// the pull request rather than the person who wrote it. A reviewer is deciding
// whether the diff is correct; the author's task framing and step-by-step plan
// are process artifacts that belong wherever they already live, so neither has a
// section here. {{task}} and {{plan}} remain substitutable for a template that
// asks for them by name — they are absent from the default, not unsupported.
const DefaultTemplate = `## Summary
{{summary}}

## Changes
{{changes}}

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

// Render substitutes {{placeholder}} values and drops any section left with no
// content. A placeholder with no local source is the ordinary case — {{testing}}
// has none on the offline path, and {{task}}/{{plan}} are empty in every
// repository that is not pinned to a memory project — and emitting a heading
// above nothing puts a hollow section in front of every reviewer.
//
// Section boundaries come from the template, not from the substituted text: a
// value that happens to begin with # is content, not a new heading.
func (t Template) Render(values map[string]string) string {
	return render(t.Text, values)
}

// StripPlaceholders drops any {{placeholder}} left in an already-written body,
// along with the section it leaves empty.
//
// A generated body needs this even though Render exists. Render substitutes into
// the template, so a placeholder with no value never reaches the output; but the
// provider is handed that same template as the style to follow, and a model that
// has nothing to say for a section can echo the placeholder back as prose rather
// than omitting it. At that point it is body text, not a placeholder awaiting a
// value, so nothing downstream would remove it and `{{testing}}` ships to the
// reviewer. Asking the model more nicely is not a fix: the body has to be
// correct whether or not it complies.
func StripPlaceholders(body string) string {
	return render(body, nil)
}

func render(text string, values map[string]string) string {
	var out, section []string
	inSection, hasContent, fenced := false, false, false

	flush := func() {
		if inSection && hasContent {
			out = append(out, section...)
		}
		section, hasContent = nil, false
	}

	for _, raw := range strings.Split(text, "\n") {
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
