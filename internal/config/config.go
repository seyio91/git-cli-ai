package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	LayerDefault = "default"
	LayerGlobal  = "global"
	LayerRepo    = "repo"
)

type Config struct {
	Commit CommitConfig `json:"commit"`
	AI     AIConfig     `json:"ai"`
	PR     PRConfig     `json:"pr"`
	Branch BranchConfig `json:"branch"`
}

type CommitConfig struct {
	Style string `json:"style"`
}

type AIConfig struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	CommitModel string `json:"commit_model"`
	PRModel     string `json:"pr_model"`
}

type PRConfig struct {
	Template string `json:"template"`
}

type BranchConfig struct {
	Pattern       string `json:"pattern"`
	DefaultBranch string `json:"default_branch"`
}

type Resolved struct {
	Config  Config            `json:"config"`
	Sources map[string]string `json:"sources"`
}

type LoadError struct {
	Message string
	Hint    string
	Details string
}

func (e *LoadError) Error() string {
	return e.Message
}

type fileConfig struct {
	Commit *fileCommitConfig `toml:"commit"`
	AI     *fileAIConfig     `toml:"ai"`
	PR     *filePRConfig     `toml:"pr"`
	Branch *fileBranchConfig `toml:"branch"`
}

type fileCommitConfig struct {
	Style *string `toml:"style"`
}

type fileAIConfig struct {
	Provider    *string `toml:"provider"`
	Model       *string `toml:"model"`
	CommitModel *string `toml:"commit_model"`
	PRModel     *string `toml:"pr_model"`
}

type filePRConfig struct {
	Template *string `toml:"template"`
}

type fileBranchConfig struct {
	Pattern       *string `toml:"pattern"`
	DefaultBranch *string `toml:"default_branch"`
}

func Load(ctx context.Context, cwd string) (Resolved, error) {
	resolved := defaultResolved()

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Resolved{}, err
		}
	}

	global, err := loadFile(globalPath())
	if err != nil {
		return Resolved{}, err
	}
	if global != nil {
		if err := apply(&resolved, global, LayerGlobal, globalPath()); err != nil {
			return Resolved{}, err
		}
	}

	if root, ok := repoRoot(ctx, cwd); ok {
		repoPath := filepath.Join(root, ".git-cli.toml")
		repo, err := loadFile(repoPath)
		if err != nil {
			return Resolved{}, err
		}
		if repo != nil {
			if err := apply(&resolved, repo, LayerRepo, repoPath); err != nil {
				return Resolved{}, err
			}
		}
	}

	return resolved, nil
}

func Defaults() Config {
	return Config{
		Commit: CommitConfig{
			Style: "conventional-commits",
		},
		AI: AIConfig{
			Provider:    "anthropic",
			CommitModel: "claude-haiku-4-5",
			PRModel:     "claude-sonnet-5",
		},
		PR: PRConfig{
			Template: "",
		},
		Branch: BranchConfig{
			Pattern:       "{type}/{slug}",
			DefaultBranch: "",
		},
	}
}

func defaultResolved() Resolved {
	sources := map[string]string{
		"commit.style":          LayerDefault,
		"ai.provider":           LayerDefault,
		"ai.model":              LayerDefault,
		"ai.commit_model":       LayerDefault,
		"ai.pr_model":           LayerDefault,
		"pr.template":           LayerDefault,
		"branch.pattern":        LayerDefault,
		"branch.default_branch": LayerDefault,
	}

	return Resolved{Config: Defaults(), Sources: sources}
}

func globalPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "git-cli", "config.toml")
}

func repoRoot(ctx context.Context, cwd string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return root, true
}

func loadFile(path string) (*fileConfig, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, &LoadError{
			Message: fmt.Sprintf("cannot read config file %s", path),
			Hint:    "check that the config file exists and is readable",
			Details: err.Error(),
		}
	}
	defer file.Close()

	cfg, err := decodeFile(path, file)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func decodeFile(path string, r io.Reader) (*fileConfig, error) {
	var cfg fileConfig
	decoder := toml.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		message := fmt.Sprintf("invalid config file %s", path)
		details := err.Error()
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			if keys := strictKeys(strict); keys != "" {
				message = fmt.Sprintf("invalid config file %s: unknown key %s", path, keys)
			}
			details = strict.String()
		}
		return nil, &LoadError{
			Message: message,
			Hint:    "fix the TOML syntax or remove unsupported config keys",
			Details: details,
		}
	}
	return &cfg, nil
}

func strictKeys(err *toml.StrictMissingError) string {
	var keys []string
	for _, decodeErr := range err.Errors {
		key := decodeErr.Key()
		if len(key) == 0 {
			continue
		}
		keys = append(keys, strings.Join([]string(key), "."))
	}
	return strings.Join(keys, ", ")
}

func apply(resolved *Resolved, cfg *fileConfig, layer string, path string) error {
	if cfg.Commit != nil && cfg.Commit.Style != nil {
		if !validCommitStyle(*cfg.Commit.Style) {
			return &LoadError{
				Message: fmt.Sprintf("invalid commit.style in %s: %q", path, *cfg.Commit.Style),
				Hint:    "set commit.style to conventional-commits, gitmoji, or freeform-with-rules",
			}
		}
		resolved.Config.Commit.Style = *cfg.Commit.Style
		resolved.Sources["commit.style"] = layer
	}

	if cfg.AI != nil {
		if cfg.AI.Provider != nil {
			resolved.Config.AI.Provider = *cfg.AI.Provider
			resolved.Sources["ai.provider"] = layer
		}
		if cfg.AI.Model != nil {
			resolved.Config.AI.Model = *cfg.AI.Model
			resolved.Sources["ai.model"] = layer
		}
		if cfg.AI.CommitModel != nil {
			resolved.Config.AI.CommitModel = *cfg.AI.CommitModel
			resolved.Sources["ai.commit_model"] = layer
		}
		if cfg.AI.PRModel != nil {
			resolved.Config.AI.PRModel = *cfg.AI.PRModel
			resolved.Sources["ai.pr_model"] = layer
		}
	}

	if cfg.PR != nil && cfg.PR.Template != nil {
		resolved.Config.PR.Template = *cfg.PR.Template
		resolved.Sources["pr.template"] = layer
	}

	if cfg.Branch != nil {
		if cfg.Branch.Pattern != nil {
			resolved.Config.Branch.Pattern = *cfg.Branch.Pattern
			resolved.Sources["branch.pattern"] = layer
		}
		if cfg.Branch.DefaultBranch != nil {
			resolved.Config.Branch.DefaultBranch = *cfg.Branch.DefaultBranch
			resolved.Sources["branch.default_branch"] = layer
		}
	}

	return nil
}

func validCommitStyle(style string) bool {
	switch style {
	case "conventional-commits", "gitmoji", "freeform-with-rules":
		return true
	default:
		return false
	}
}
