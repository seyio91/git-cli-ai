package pr

import "testing"

// Fills decides whether a supplied body is used verbatim or handed to a
// generator that replaces it. Both directions have a cost, but they are not
// symmetric: a false positive posts an unfinished body, while a false negative
// silently discards prose a human deliberately wrote. The tests below pin both.

func TestFillsRejectsAnUnfilledTemplate(t *testing.T) {
	tmpl := Template{Text: DefaultTemplate}

	// Reading the repo's own template and passing it through is a natural
	// thing for a person or an agent to do. Every heading matches, so a
	// heading-only check calls it conforming and posts the placeholders.
	if tmpl.Fills(DefaultTemplate) {
		t.Fatal("the unfilled template was accepted as a finished body")
	}

	if !tmpl.Fills(filled) {
		t.Fatal("a genuinely filled body was rejected")
	}
}

func TestFillsRejectsSectionsWithNoContent(t *testing.T) {
	tmpl := Template{Text: DefaultTemplate}

	// Headings present, one section empty. That is a half-written body, not a
	// finished one.
	body := "## Summary\nDoes the thing.\n\n## Changes\n- a.txt\n\n## Task\n\n## Plan\nnone\n\n## Testing\nran it\n"
	if tmpl.Fills(body) {
		t.Fatal("a body with an empty section was accepted as finished")
	}
}

// A near-miss must not cost the author their text. Case, heading level and
// trailing punctuation are all cosmetic; treating them as structural means a
// complete hand-written body gets replaced by generated prose.
func TestFillsToleratesCosmeticHeadingDifferences(t *testing.T) {
	tmpl := Template{Text: DefaultTemplate}

	for _, tc := range []struct{ name, body string }{
		{"lowercase", "## summary\nx\n\n## changes\nx\n\n## task\nx\n\n## plan\nx\n\n## testing\nx\n"},
		{"different level", "### Summary\nx\n\n### Changes\nx\n\n### Task\nx\n\n### Plan\nx\n\n### Testing\nx\n"},
		{"trailing colon", "## Summary:\nx\n\n## Changes:\nx\n\n## Task:\nx\n\n## Plan:\nx\n\n## Testing:\nx\n"},
		{"extra inner space", "##  Summary\nx\n\n##  Changes\nx\n\n##  Task\nx\n\n##  Plan\nx\n\n##  Testing\nx\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tmpl.Fills(tc.body) {
				t.Fatal("a complete body was rejected over a cosmetic heading difference; the author's text would be replaced by generated prose")
			}
		})
	}
}

func TestFillsStillRejectsAMissingSection(t *testing.T) {
	tmpl := Template{Text: DefaultTemplate}

	body := "## Summary\nDoes the thing.\n\n## Changes\n- a.txt\n"
	if tmpl.Fills(body) {
		t.Fatal("a body missing three sections was accepted")
	}
}

// A `#` inside a fenced block is a shell comment, not a section the author has
// to reproduce.
func TestHeadingsIgnoresFencedCode(t *testing.T) {
	tmpl := Template{Text: "## Summary\n{{summary}}\n\n```sh\n# not a heading\ngit push\n```\n"}

	headings := tmpl.Headings()
	if len(headings) != 1 {
		t.Fatalf("headings = %#v, want only the real one", headings)
	}
}

const filled = `## Summary
Does the thing.

## Changes
- a.txt

## Task
JIRA-1

## Plan
plans/thing.md

## Testing
ran the suite
`
