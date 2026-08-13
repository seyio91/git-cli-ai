package cli

import "testing"

func TestVersionInfoString(t *testing.T) {
	cases := []struct {
		name string
		info versionInfo
		want string
	}{
		{"version only", versionInfo{Version: "dev"}, "dev"},
		{"with commit", versionInfo{Version: "v1.2.0", Commit: "a461b0dfd6e4"}, "v1.2.0 (a461b0dfd6e4)"},
		{"with dirty", versionInfo{Version: "v1.2.0", Commit: "a461b0dfd6e4", Dirty: true}, "v1.2.0 (a461b0dfd6e4, dirty)"},
		{"long commit truncated to 12", versionInfo{Version: "dev", Commit: "a461b0dfd6e4b79d9d90a68b03c122e616b52c8a"}, "dev (a461b0dfd6e4)"},
		{"dirty without commit shows no marker", versionInfo{Version: "dev", Dirty: true}, "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
