package branch

import "testing"

// SC-30 — a pattern that cannot produce a distinct branch name is rejected.
func TestValidatePattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{"default", "{type}/{slug}", false},
		{"slug only", "{slug}", false},
		{"type only", "{type}", false},
		{"prefixed", "wip/{type}-{slug}", false},
		{"no placeholder at all", "no-placeholders-at-all", true},
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"unknown placeholder", "{author}/{slug}", true},
		{"unknown placeholder alone", "{nope}", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePattern(tc.pattern)
			if tc.wantErr && err == nil {
				t.Errorf("ValidatePattern(%q) = nil, want an error", tc.pattern)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidatePattern(%q) = %v, want nil", tc.pattern, err)
			}
		})
	}
}

func TestName(t *testing.T) {
	cases := []struct {
		name       string
		pattern    string
		commitType string
		subject    string
		want       string
	}{
		{"conventional", "{type}/{slug}", "feat", "add the widget", "feat/add-the-widget"},
		{"punctuation collapses", "{type}/{slug}", "fix", "handle  A -> B, again!", "fix/handle-a-b-again"},
		{"uppercase folded", "{type}/{slug}", "feat", "Add The Widget", "feat/add-the-widget"},
		// SC-31: a style with no type must still yield a usable name, not one
		// with a leading separator.
		{"empty type collapses its segment", "{type}/{slug}", "", "add the widget", "add-the-widget"},
		{"empty type, type-only pattern", "{type}", "", "add the widget", "change"},
		// Letters are kept whatever the script: folding to ASCII would
		// reduce a non-Latin subject to nothing. Symbols still become
		// separators.
		{"non-ascii letters survive", "{type}/{slug}", "feat", "café ☕ support", "feat/café-support"},
		{"non-latin script survives", "{type}/{slug}", "feat", "ウィジェットを追加", "feat/ウィジェットを追加"},
		{"subject with no slug-able runes", "{type}/{slug}", "chore", "!!! ???", "chore"},
		{"everything empty", "{type}/{slug}", "", "", "change"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Name(tc.pattern, tc.commitType, tc.subject); got != tc.want {
				t.Errorf("Name(%q, %q, %q) = %q, want %q", tc.pattern, tc.commitType, tc.subject, got, tc.want)
			}
		})
	}
}

// A long subject is cut at a hyphen boundary, never mid-word.
func TestSlugTruncatesAtAWordBoundary(t *testing.T) {
	subject := "add the widget that turns the sprocket which drives the flywheel assembly"

	got := Slug(subject)
	if len(got) > maxSlug {
		t.Errorf("slug is %d bytes, over the %d cap: %q", len(got), maxSlug, got)
	}
	if got[len(got)-1] == '-' {
		t.Errorf("slug ends on a separator: %q", got)
	}
	// Cutting mid-word would leave a fragment; every retained token must be a
	// whole word from the subject.
	if want := "add-the-widget-that-turns-the-sprocket-which"; got != want {
		t.Errorf("Slug() = %q, want %q", got, want)
	}
}

// A single word longer than the cap has no boundary to cut at; it is truncated
// rather than dropped, because a branch still has to be named.
func TestSlugHandlesAnUncuttableWord(t *testing.T) {
	got := Slug(stringOfLen(80))
	if len(got) > maxSlug {
		t.Errorf("slug is %d bytes, over the %d cap", len(got), maxSlug)
	}
	if got == "" {
		t.Error("slug is empty; a branch still needs a name")
	}
}

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
