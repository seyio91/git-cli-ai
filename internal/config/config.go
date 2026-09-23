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

	"github.com/seyio91/git-cli-ai/internal/branch"
	"github.com/seyio91/git-cli-ai/internal/conventional"
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
	// Types is the closed set of conventional commit types. Empty means the
	// open vocabulary, which is what every repo that sets nothing keeps: the
	// validator has always checked the shape of a type and never its name, and
	// closing that by default would change what an existing repo accepts.
	Types []string `json:"types,omitempty"`
}

type AIConfig struct {
	Provider    string                    `json:"provider"`
	Model       string                    `json:"model"`
	CommitModel string                    `json:"commit_model"`
	PRModel     string                    `json:"pr_model"`
	Providers   map[string]ProviderConfig `json:"providers,omitempty"`
}

// ProviderConfig is one named profile under [ai.providers]. There is no
// api_key field by design: key material is only ever read from the environment
// named by APIKeyEnv.
type ProviderConfig struct {
	Type      string `json:"type"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	Model     string `json:"model,omitempty"`
	// MaxTokensParam names the field that carries the output limit. OpenAI's
	// newer models reject max_tokens and require max_completion_tokens, while
	// the other endpoints this type serves — Ollama, Groq, OpenRouter — still
	// take max_tokens. There is no reliable way to infer which from a model
	// id, so the profile states it. Empty means max_tokens.
	MaxTokensParam string   `json:"max_tokens_param,omitempty"`
	Command        []string `json:"command,omitempty"`
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

// Types is a pointer so that `types = []` is distinguishable from an absent
// key. They mean opposite things and the difference has to survive decoding:
// absent is the open vocabulary, and an empty list allows nothing at all.
type fileCommitConfig struct {
	Style *string   `toml:"style"`
	Types *[]string `toml:"types"`
}

type fileAIConfig struct {
	Provider    *string                       `toml:"provider"`
	Model       *string                       `toml:"model"`
	CommitModel *string                       `toml:"commit_model"`
	PRModel     *string                       `toml:"pr_model"`
	Providers   map[string]fileProviderConfig `toml:"providers"`
}

// APIKey is declared only so that an inline key is rejected by name rather than
// falling through to the strict-unknown-key path, whose error would quote the
// offending source line back at the caller.
type fileProviderConfig struct {
	Type           *string  `toml:"type"`
	BaseURL        *string  `toml:"base_url"`
	APIKeyEnv      *string  `toml:"api_key_env"`
	Model          *string  `toml:"model"`
	MaxTokensParam *string  `toml:"max_tokens_param"`
	Command        []string `toml:"command"`
	APIKey         *string  `toml:"api_key"`
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
			// gpt-4.1-mini is the default because it is fast and cheap enough
			// for the commit hot path and accepts max_tokens. OpenAI's newer
			// models reject that parameter; point a profile at one of those and
			// set max_tokens_param = "max_completion_tokens".
			Provider:    "openai",
			CommitModel: "gpt-4.1-mini",
			PRModel:     "gpt-4.1",
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
		"commit.types":          LayerDefault,
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
		// strict.String() annotates the offending source lines, which would echo
		// a secret back to the caller if the unknown key happened to hold one.
		// The key and the file are already named in the message.
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			if keys := strictKeys(strict); keys != "" {
				message = fmt.Sprintf("invalid config file %s: unknown key %s", path, keys)
			}
			details = ""
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
	if cfg.Commit != nil {
		if cfg.Commit.Style != nil {
			if !validCommitStyle(*cfg.Commit.Style) {
				return &LoadError{
					Message: fmt.Sprintf("invalid commit.style in %s: %q", path, *cfg.Commit.Style),
					Hint:    "set commit.style to conventional-commits, gitmoji, or freeform-with-rules",
				}
			}
			resolved.Config.Commit.Style = *cfg.Commit.Style
			resolved.Sources["commit.style"] = layer
		}
		if cfg.Commit.Types != nil {
			// Checked here for the same reason branch.pattern is: the error
			// names the file that set it, and arrives before a billable
			// generation rather than after one.
			if err := validCommitTypes(*cfg.Commit.Types); err != nil {
				return &LoadError{
					Message: fmt.Sprintf("invalid commit.types in %s: %s", path, err),
					Hint:    "list the types you allow, for example [\"feat\", \"fix\", \"chore\"]; remove the key entirely to accept any type",
				}
			}
			resolved.Config.Commit.Types = *cfg.Commit.Types
			resolved.Sources["commit.types"] = layer
		}
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
		if err := applyProviders(resolved, cfg.AI.Providers, layer, path); err != nil {
			return err
		}
	}

	if cfg.PR != nil && cfg.PR.Template != nil {
		resolved.Config.PR.Template = *cfg.PR.Template
		resolved.Sources["pr.template"] = layer
	}

	if cfg.Branch != nil {
		if cfg.Branch.Pattern != nil {
			// Checked here rather than where ship renders it: the error then
			// names the file that set it, and arrives before a billable
			// message generation rather than after one.
			if err := branch.ValidatePattern(*cfg.Branch.Pattern); err != nil {
				return &LoadError{
					Message: fmt.Sprintf("invalid branch.pattern in %s: %q: %s", path, *cfg.Branch.Pattern, err),
					Hint:    "branch.pattern must contain {type} and/or {slug}, for example \"{type}/{slug}\"",
				}
			}
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

// applyProviders merges provider profiles key by key, so a repo file that sets
// one field of a profile keeps the fields an earlier layer supplied.
func applyProviders(resolved *Resolved, profiles map[string]fileProviderConfig, layer string, path string) error {
	for name, profile := range profiles {
		if profile.APIKey != nil {
			return &LoadError{
				Message: fmt.Sprintf("invalid config file %s: api_key is not permitted in [ai.providers.%s]", path, name),
				Hint:    fmt.Sprintf("remove api_key and set api_key_env to the name of an environment variable holding the key; rotate the key that was written to %s", path),
			}
		}

		if resolved.Config.AI.Providers == nil {
			resolved.Config.AI.Providers = map[string]ProviderConfig{}
		}
		current := resolved.Config.AI.Providers[name]

		prefix := "ai.providers." + name + "."
		if profile.Type != nil {
			if !validProviderType(*profile.Type) {
				return &LoadError{
					Message: fmt.Sprintf("invalid ai.providers.%s.type in %s: %q", name, path, *profile.Type),
					Hint:    "set type to anthropic, openai-compat, or cli",
				}
			}
			current.Type = *profile.Type
			resolved.Sources[prefix+"type"] = layer
		}
		if profile.BaseURL != nil {
			current.BaseURL = *profile.BaseURL
			resolved.Sources[prefix+"base_url"] = layer
		}
		if profile.APIKeyEnv != nil {
			current.APIKeyEnv = *profile.APIKeyEnv
			resolved.Sources[prefix+"api_key_env"] = layer
		}
		if profile.Model != nil {
			current.Model = *profile.Model
			resolved.Sources[prefix+"model"] = layer
		}
		if profile.MaxTokensParam != nil {
			current.MaxTokensParam = *profile.MaxTokensParam
			resolved.Sources[prefix+"max_tokens_param"] = layer
		}
		if profile.Command != nil {
			current.Command = profile.Command
			resolved.Sources[prefix+"command"] = layer
		}

		resolved.Config.AI.Providers[name] = current
	}

	return nil
}

func validProviderType(providerType string) bool {
	switch providerType {
	case "anthropic", "openai-compat", "cli":
		return true
	default:
		return false
	}
}

// validCommitTypes rejects a vocabulary that cannot do what it was written to
// do. An empty list is refused rather than read as "any type": a list that
// allows nothing is never what anyone meant, and silently treating it as the
// open vocabulary would be the widest possible reading of the narrowest
// possible instruction.
func validCommitTypes(types []string) error {
	if len(types) == 0 {
		return errors.New("the list is empty, which would allow no type at all")
	}

	seen := make(map[string]bool, len(types))
	for _, t := range types {
		if !conventional.ValidType(t) {
			return fmt.Errorf("%q is not a valid type: a type starts with a lowercase letter and contains only lowercase letters, digits, or hyphens", t)
		}
		if seen[t] {
			return fmt.Errorf("%q is listed more than once", t)
		}
		seen[t] = true
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
