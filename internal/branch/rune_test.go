package branch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSlugNeverSplitsARune(t *testing.T) {
	// 30 three-byte runes = 90 bytes, no separators anywhere: the only cut
	// available is a byte offset inside the cap.
	subject := strings.Repeat("追", 30)

	got := Slug(subject)
	if !utf8.ValidString(got) {
		t.Errorf("slug is not valid UTF-8: %q", got)
	}
	if len(got) > maxSlug {
		t.Errorf("slug is %d bytes, over the %d cap", len(got), maxSlug)
	}
}
