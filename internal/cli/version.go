package cli

import (
	"encoding/json"
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"
)

const shortCommitLen = 12

// versionInfo is what `version` reports. Commit and Dirty come from the build's
// embedded VCS data, so they are populated for any build made from the repo,
// with or without a release ldflags stamp.
type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"`
}

// buildVersion resolves the version to report. The ldflags-stamped value wins;
// failing that, a module version from `go install <module>@<tag>` is used; a
// plain build stays "dev". The commit and dirty flag are always read from the
// embedded build info.
func buildVersion(stamped string) versionInfo {
	info := versionInfo{Version: stamped}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}

	if stamped == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
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
