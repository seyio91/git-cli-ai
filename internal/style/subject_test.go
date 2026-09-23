package style

import "testing"

func TestSubject(t *testing.T) {
	cases := []struct {
		style string
		msg   string
		want  string
	}{
		{Conventional, "feat(api): add endpoint", "add endpoint"},
		{Conventional, "feat!: breaking", "breaking"},
		{Conventional, "feat(api)!: scoped breaking", "scoped breaking"},
		{Conventional, "fix: repair: nested colon", "repair: nested colon"},
		{Gitmoji, ":sparkles: feat(api): add x", "add x"},
		{Gitmoji, ":bug: fix the bug", "fix the bug"},
		{Gitmoji, "🎨 feat(api): add x", "add x"},
		{Freeform, "just some freeform text", "just some freeform text"},
	}
	for _, c := range cases {
		v, err := For(c.style, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := v.Subject(c.msg); got != c.want {
			t.Errorf("%s Subject(%q) = %q, want %q", c.style, c.msg, got, c.want)
		}
	}
}
