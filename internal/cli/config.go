package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	appconfig "github.com/seyio91/git-cli-ai/internal/config"
	"github.com/spf13/cobra"
)

func NewConfigCommand(root *Options, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print resolved configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfig(cmd.Context(), root, out)
		},
	}
	return cmd
}

func runConfig(ctx context.Context, root *Options, out io.Writer) error {
	resolved, err := appconfig.Load(ctx, "")
	if err != nil {
		return err
	}
	return writeConfigPayload(out, root.JSON, resolved)
}

func writeConfigPayload(out io.Writer, asJSON bool, resolved appconfig.Resolved) error {
	if asJSON {
		return json.NewEncoder(out).Encode(resolved)
	}

	printValue := func(path string, value string) {
		_, _ = fmt.Fprintf(out, "%s = %q (%s)\n", path, value, resolved.Sources[path])
	}

	printValue("commit.style", resolved.Config.Commit.Style)
	// A list is printed unquoted, and an unset one prints as [] rather than
	// being omitted: "any type is accepted" is a real state of this setting,
	// and leaving the line out would read as the setting not existing.
	_, _ = fmt.Fprintf(out, "commit.types = [%s] (%s)\n",
		strings.Join(resolved.Config.Commit.Types, ", "), resolved.Sources["commit.types"])
	printValue("ai.provider", resolved.Config.AI.Provider)
	printValue("ai.model", resolved.Config.AI.Model)
	printValue("ai.commit_model", resolved.Config.AI.CommitModel)
	printValue("ai.pr_model", resolved.Config.AI.PRModel)
	printValue("pr.template", resolved.Config.PR.Template)
	printValue("branch.pattern", resolved.Config.Branch.Pattern)
	printValue("branch.default_branch", resolved.Config.Branch.DefaultBranch)
	return nil
}
