// Package pr renders pull request bodies and opens pull requests through a
// forge provider. Nothing in this package can merge: the Provider interface has
// no merge method and the gh implementation builds no such argv.
package pr

import (
	"os"
	"regexp"
	"strings"
)

// DefaultTemplate is the built-in body structure, shaped for the person reading
// the pull request rather than the person who wrote it. A reviewer is deciding
// whether the diff is correct; the author's task framing and step-by-step plan
// are process artifacts that belong wherever they already live, so neither has a
// section here. {{task}}, {{plan}} and {{testing}} remain substitutable for a
// template that asks for them by name — they are absent from the default, not
// unsupported.
//
// Testing left the default because CI reports it. A hand-written claim that the
// tests pass duplicates a status check when it is right and contradicts one when
// it is wrong, which is the same objection that removed Task and Plan. The
// offline path could not fill it anyway: {{testing}} has no local source, so the
// section was already dropped on every provider-less render.
//
// Prerequisites and Ordering are safe to ship by default even though most pull
// requests have neither, because a section with nothing in it never reaches the
// body: an unfilled placeholder is dropped at render, and a provider that
// answers "None." is dropped by StripPlaceholders. They cost nothing when
// irrelevant and they carry the two things no tool can derive — a cross-repo
// dependency, and which pull request has to merge first.
//
// {{summary}} keeps its name under a heading called Description. The placeholder
// names are the interface the command layer fills; the headings are what a
// reviewer reads. Renaming the placeholder to match the heading would break
// every custom pr.template in existence for no gain.
const DefaultTemplate = `## Description
{{summary}}

## Changes
{{changes}}

## Prerequisites
{{prerequisites}}

## Ordering
{{ordering}}
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
	return render(t.Text, values, false)
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
// It also drops a section whose entire content is a denial that there is
// anything to report. Telling the provider to omit an inapplicable section does
// not hold on its own: measured against a live generation after the instruction
// was made explicit, the model still answered "None." under both headings it had
// nothing for. Such a section is content by every structural measure, so the
// heading survives and the reviewer reads two words that say nothing.
func StripPlaceholders(body string) string {
	return render(body, nil, true)
}

// denial matches a line that opens by reporting an absence. It matches a leading
// clause rather than the whole line because the first version was anchored at
// both ends and real output walked straight past it: a live body carried
// "None. Self-contained, no secrets or tags involved." and "None, can merge on
// its own.", neither of which is only a denial, and both sections survived to the
// reviewer.
//
// Two things keep it from eating real content. The denial must close within
// three words, on punctuation or end of line — so "No caller outside
// internal/cli reaches this path any more, so the export is gone." does not
// match, its first comma being eight words in. And the pattern is anchored with
// no list marker, so a genuine change line that opens with "no"
// ("- no longer reads the env var") is exempt.
var denial = regexp.MustCompile(`(?i)^(none|n/?a|nothing|not applicable|no)(\s+\S+){0,3}\s*([.,;:]|$)`)

// hollow reports whether a section's body, excluding its heading, says only that
// there is nothing to report. Only a lone line qualifies — a section with two or
// more lines is carrying something.
func hollow(section []string) bool {
	var content []string
	for _, line := range section[1:] {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			content = append(content, trimmed)
		}
	}
	return len(content) == 1 && denial.MatchString(content[0])
}

func render(text string, values map[string]string, dropHollow bool) string {
	var out, section []string
	inSection, hasContent, fenced := false, false, false

	flush := func() {
		if inSection && hasContent && !(dropHollow && hollow(section)) {
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
