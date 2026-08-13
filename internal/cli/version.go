package cli

import (
	"encoding/json"
	"io"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

const shortCommitLen = 12

// pseudoVersion matches the trailing <yyyymmddhhmmss>-<12 hex> that every Go
// module pseudo-version ends with, across all three of its forms:
// v0.0.0-<ts>-<sha>, v1.2.3-0.<ts>-<sha>, and v1.2.3-pre.0.<ts>-<sha>.
var pseudoVersion = regexp.MustCompile(`[-.][0-9]{14}-[0-9a-f]{12}$`)

// releaseVersion reports whether a module version is a real release — something
// a tag actually named — rather than a placeholder the toolchain synthesised.
//
// This is load-bearing, not defensive. A plain `go build` from a clean VCS tree
// with no reachable semver tag gets a pseudo-version stamped into
// bi.Main.Version, so "is it (devel)?" no longer distinguishes an install at a
// tag from an ordinary local build — and reporting v0.0.0-<ts>-<sha> as the
// version contradicts what a plain build is documented to say. Nothing is lost by
// declining it: the commit it encodes is already reported in its own field.
func releaseVersion(v string) bool {
	if v == "" || v == "(devel)" {
		return false
	}

	// Build metadata is not part of a version's identity, and it is not optional
	// to handle: a build from a modified tree gets "+dirty" appended to the
	// pseudo-version, which would otherwise slip past the match and be reported
	// as a release.
	if plus := strings.IndexByte(v, '+'); plus >= 0 {
		v = v[:plus]
	}
	return !pseudoVersion.MatchString(v)
}

// versionInfo is what `version` reports. Commit and Dirty come from the build's
// embedded VCS data, so they are populated for any build made from the repo,
// with or without a release ldflags stamp.
type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"`
}

// buildVersion resolves the version to report. The ldflags-stamped value wins;
// failing that, a released module version from `go install <module>@<tag>` is
// used; a plain build stays "dev". The commit and dirty flag are always read from
// the embedded build info.
func buildVersion(stamped string) versionInfo {
	info := versionInfo{Version: stamped}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}

	if stamped == "dev" && releaseVersion(bi.Main.Version) {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info
}

// String renders a human line: "v1.2.0 (a461b0dfd6e4, dirty)".
func (v versionInfo) String() string {
	s := v.Version
	if v.Commit != "" {
		commit := v.Commit
		if len(commit) > shortCommitLen {
			commit = commit[:shortCommitLen]
		}
		s += " (" + commit
		if v.Dirty {
			s += ", dirty"
		}
		s += ")"
	}
	return s
}

func newVersionCommand(opts *Options, stamped string, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the git-cli version and the commit it was built from",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := buildVersion(stamped)
			if opts.JSON {
				return json.NewEncoder(out).Encode(info)
			}
			_, err := io.WriteString(out, info.String()+"\n")
			return err
		},
	}
}
