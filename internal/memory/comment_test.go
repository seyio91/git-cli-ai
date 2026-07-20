package memory

import "testing"

func TestSectionSkipsHTMLComments(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{
			name: "multi-line comment with a heading inside does not leak or terminate early",
			body: "## Current Goal\nreal goal\n\n<!-- template note\n## Related Projects\n| a | b |\n-->\n\nstill the goal\n",
			want: "real goal\n\n\nstill the goal",
		},
		{
			name: "comment with no heading inside does not leak",
			body: "## Current Goal\nreal goal\n<!-- just a note\nmore note\n-->\n",
			want: "real goal",
		},
		{
			name: "single-line comment is skipped",
			body: "## Current Goal\nreal goal\n<!-- inline -->\ntail\n",
			want: "real goal\ntail",
		},
		{
			name: "a real later heading still terminates the section",
			body: "## Current Goal\nreal goal\n## Next\nnope\n",
			want: "real goal",
		},
		{
			name: "comment markers inside a fence are literal content",
			body: "## Current Goal\n```\n<!-- kept\n```\ntail\n",
			want: "```\n<!-- kept\n```\ntail",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionFrom(tc.body, "current goal"); got != tc.want {
				t.Errorf("section() = %q, want %q", got, tc.want)
			}
		})
	}
}
