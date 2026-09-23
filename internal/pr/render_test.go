package pr

import (
	"strings"
	"testing"
)

func TestRenderDropsEmptySections(t *testing.T) {
	tmpl := Template{Text: DefaultTemplate}

	body := tmpl.Render(map[string]string{
		"summary": "Does the thing.",
		"changes": "- a.txt",
	})

	// Task and Plan are not in the default template at all; asserting their
	// absence here is what keeps them from drifting back in.
	for _, gone := range []string{"## Task", "## Plan", "## Testing", "{{"} {
		if strings.Contains(body, gone) {
			t.Errorf("rendered body still contains %q:\n%s", gone, body)
		}
	}
	for _, kept := range []string{"## Summary", "Does the thing.", "## Changes", "- a.txt"} {
		if !strings.Contains(body, kept) {
			t.Errorf("rendered body lost %q:\n%s", kept, body)
		}
	}
}

func TestRenderKeepsSectionsThatHaveValues(t *testing.T) {
	tmpl := Template{Text: DefaultTemplate}

	body := tmpl.Render(map[string]string{"summary": "s", "changes": "c", "testing": "go test"})

	for _, kept := range []string{"## Summary", "s", "## Changes", "c", "## Testing", "go test"} {
		if !strings.Contains(body, kept) {
			t.Errorf("rendered body lost %q:\n%s", kept, body)
		}
	}
}

// {{task}} and {{plan}} left the default template, but they are still
// substitutable: a repository that wants them says so in its own pr.template,
// and the memory context still supplies the values.
func TestRenderStillSubstitutesTaskAndPlanInACustomTemplate(t *testing.T) {
	tmpl := Template{Text: "## Summary\n{{summary}}\n\n## Task\n{{task}}\n\n## Plan\n{{plan}}\n"}

	body := tmpl.Render(map[string]string{"summary": "s", "task": "P5.3", "plan": "phase5"})

	for _, kept := range []string{"## Task", "P5.3", "## Plan", "phase5"} {
		if !strings.Contains(body, kept) {
			t.Errorf("a custom template lost %q:\n%s", kept, body)
		}
	}
}

// A section carrying literal text survives even when its placeholder is empty:
// the author wrote that text, and it is content.
func TestRenderKeepsLiteralContentWhenPlaceholderIsEmpty(t *testing.T) {
	tmpl := Template{Text: "## Checklist\nAlways review this.\n{{extra}}\n"}

	body := tmpl.Render(map[string]string{})
	if !strings.Contains(body, "## Checklist") || !strings.Contains(body, "Always review this.") {
		t.Errorf("dropped a section that had literal content:\n%s", body)
	}
}

// A value beginning with # is content, not a heading, so it must not split the
// section it belongs to or resurrect a later one.
func TestRenderDoesNotTreatValuesAsHeadings(t *testing.T) {
	tmpl := Template{Text: "## Summary\n{{summary}}\n\n## Task\n{{task}}\n"}

	body := tmpl.Render(map[string]string{"summary": "# not a heading"})
	if strings.Contains(body, "## Task") {
		t.Errorf("a value starting with # resurrected a later empty section:\n%s", body)
	}
	if !strings.Contains(body, "# not a heading") {
		t.Errorf("lost the value:\n%s", body)
	}
}

// Headings inside fenced code are not section boundaries.
func TestRenderIgnoresFencedHeadings(t *testing.T) {
	tmpl := Template{Text: "## Summary\n{{summary}}\n\n```sh\n# not a heading\n```\n"}

	body := tmpl.Render(map[string]string{"summary": "s"})
	if !strings.Contains(body, "# not a heading") {
		t.Errorf("dropped fenced content:\n%s", body)
	}
}

// A generated body reached a reviewer with "## Prerequisites\n\nNone." and
// "## Ordering\n\nNone." under a template whose preamble told the provider to
// omit an inapplicable section outright. The instruction did not hold, so the
// drop happens here instead.
func TestStripPlaceholdersDropsSectionsThatReportNothing(t *testing.T) {
	body := StripPlaceholders(
		"## Description\n\nPrints a greeting.\n\n" +
			"## Changes\n\n- main() prints \"hi\"\n\n" +
			"## Prerequisites\n\nNone.\n\n" +
			"## Ordering\n\nNone.\n")

	for _, gone := range []string{"## Prerequisites", "## Ordering", "None."} {
		if strings.Contains(body, gone) {
			t.Errorf("body still contains %q:\n%s", gone, body)
		}
	}
	for _, kept := range []string{"## Description", "Prints a greeting.", "## Changes", "- main() prints"} {
		if !strings.Contains(body, kept) {
			t.Errorf("body lost %q:\n%s", kept, body)
		}
	}
}

// The denial pattern has to leave real content alone. A change line may open
// with "no", and a section carrying more than one line is saying something even
// if its first line is short.
func TestStripPlaceholdersKeepsContentThatMerelyLooksLikeADenial(t *testing.T) {
	cases := map[string]string{
		"list item opening with no":  "## Changes\n\n- no longer reads ANTHROPIC_BASE_URL\n",
		"denial beside real content": "## Ordering\n\nNone.\n- but tpe-kubernetes#8460 must follow\n",
		"prose that starts with no":  "## Description\n\nNo caller outside internal/cli reaches this path any more, so the export is gone.\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if got := StripPlaceholders(body); strings.TrimSpace(got) != strings.TrimSpace(body) {
				t.Errorf("StripPlaceholders altered content it should have kept:\nin:  %q\nout: %q", body, got)
			}
		})
	}
}
