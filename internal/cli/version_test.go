package cli

import "testing"

// A pseudo-version is what the toolchain synthesises for a build with no
// reachable tag, so promoting one would report a version nobody released — and
// would make `version` disagree with itself depending on whether the working tree
// happened to be dirty at build time.
func TestReleaseVersionRejectsPseudoVersions(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    bool
	}{
		{"empty", "", false},
		{"devel", "(devel)", false},
		{"bare pseudo-version", "v0.0.0-20260813132830-abd91fbc3a87", false},
		{"pseudo-version after a tag", "v1.2.3-0.20260813132830-abd91fbc3a87", false},
		{"pseudo-version after a prerelease tag", "v1.2.3-pre.0.20260813132830-abd91fbc3a87", false},
		// What a build from a modified tree actually produces.
		{"dirty pseudo-version", "v0.0.0-20260813132830-abd91fbc3a87+dirty", false},
		{"dirty pseudo-version after a tag", "v1.2.3-0.20260813132830-abd91fbc3a87+dirty", false},
		{"release tag", "v1.2.0", true},
		{"prerelease tag", "v1.2.0-rc.1", true},
		{"tag with build metadata", "v1.2.0+incompatible", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseVersion(tc.version); got != tc.want {
				t.Errorf("releaseVersion(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

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
