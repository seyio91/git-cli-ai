// Package branch derives a branch name from a commit message and the
// configured branch.pattern.
package branch

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Placeholders a pattern may contain. A pattern naming anything else, or
// naming nothing at all, cannot produce a distinct branch name and is rejected
// when config resolves rather than midway through a ship.
const (
	PlaceholderType = "{type}"
	PlaceholderSlug = "{slug}"
)

// maxSlug caps the derived slug. Long branch names are awkward in every UI that
// shows them, and the subject's opening words carry the meaning.
const maxSlug = 50

// fallbackSlug is used when a subject has no slug-able characters at all — an
// all-punctuation or all-emoji subject still has to produce a usable name.
const fallbackSlug = "change"

// ValidatePattern reports whether a pattern can produce a branch name. A
// pattern with no placeholder would give every branch the same name, and one
// with an unknown placeholder would emit it literally.
func ValidatePattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("pattern is empty")
	}

	for _, name := range placeholders(pattern) {
		if name != PlaceholderType && name != PlaceholderSlug {
			return fmt.Errorf("unknown placeholder %s", name)
		}
	}

	if !strings.Contains(pattern, PlaceholderType) && !strings.Contains(pattern, PlaceholderSlug) {
		return fmt.Errorf("pattern contains no placeholder, so every branch would be named %q", pattern)
	}
	return nil
}

// Name renders the pattern for a commit type and subject. An empty type
// collapses its segment rather than leaving a leading or doubled separator, so
// a style that supplies no type still produces a clean name.
func Name(pattern, commitType, subject string) string {
	name := strings.ReplaceAll(pattern, PlaceholderType, Slug(commitType))
	name = strings.ReplaceAll(name, PlaceholderSlug, Slug(subject))
	return tidy(name)
}

// Slug reduces text to lowercase letters and digits joined by hyphens. Letters
// are kept whatever the script — folding to ASCII would reduce a non-Latin
// subject to an empty slug — while symbols and punctuation become separators.
func Slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	slug := trimHyphens(b.String())
	if slug == "" {
		return ""
	}
	return truncate(slug)
}

// truncate cuts at a hyphen boundary so the slug never ends mid-word. Failing
// that — a single word longer than the cap — it cuts at a rune boundary, since
// slugs may hold multi-byte letters and a byte-offset cut would emit invalid
// UTF-8.
func truncate(slug string) string {
	if len(slug) <= maxSlug {
		return slug
	}
	if cut := strings.LastIndexByte(slug[:maxSlug+1], '-'); cut > 0 {
		return trimHyphens(slug[:cut])
	}

	cut := maxSlug
	for cut > 0 && !utf8.RuneStart(slug[cut]) {
		cut--
	}
	return trimHyphens(slug[:cut])
}

// tidy collapses the separators an empty placeholder leaves behind and
// guarantees a non-empty result.
func tidy(name string) string {
	for strings.Contains(name, "//") {
		name = strings.ReplaceAll(name, "//", "/")
	}
	name = strings.Trim(name, "/-")
	if name == "" {
		return fallbackSlug
	}
	return name
}

func trimHyphens(s string) string {
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// placeholders lists every {token} in the pattern, including unknown ones.
func placeholders(pattern string) []string {
	var found []string
	rest := pattern
	for {
		_, after, ok := strings.Cut(rest, "{")
		if !ok {
			return found
		}
		name, remainder, ok := strings.Cut(after, "}")
		if !ok {
			return found
		}
		found = append(found, "{"+name+"}")
		rest = remainder
	}
}
