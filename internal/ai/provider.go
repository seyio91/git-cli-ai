package ai

import (
	"fmt"
	"os"
	"sort"
	"strings"

	appconfig "github.com/seyio91/git-cli-ai/internal/config"
)

const (
	TypeAnthropic    = "anthropic"
	TypeOpenAICompat = "openai-compat"
	TypeCLI          = "cli"
)

// New builds the generator named by [ai].provider. Every failure here is a
// configuration failure and is reported before any request is attempted, so an
// unset key never turns into an unauthenticated call.
func New(cfg appconfig.Config) (Generator, error) {
	return NewFor(cfg, KindCommit)
}

// NewFor is New with the request kind stated, so a built-in profile resolves to
// the model configured for that task rather than always the commit model.
func NewFor(cfg appconfig.Config, kind string) (Generator, error) {
	name := cfg.AI.Provider
	if name == "" {
		return nil, &ProviderError{
			Message: "no ai provider is configured",
			Hint:    "set [ai].provider to the name of a profile defined under [ai.providers]",
		}
	}

	profile, ok := profileFor(cfg, name, kind)
	if !ok {
		return nil, &ProviderError{
			Provider: name,
			Message:  fmt.Sprintf("unknown ai provider %q", name),
			Hint:     unknownProviderHint(name, cfg.AI.Providers),
		}
	}

	switch profile.Type {
	case TypeCLI:
		if len(profile.Command) == 0 {
			return nil, &ProviderError{
				Provider: name,
				Message:  fmt.Sprintf("ai provider %q has no command", name),
				Hint:     fmt.Sprintf("set command in [ai.providers.%s] to an argv array, for example command = [\"my-tool\", \"--quiet\"]", name),
			}
		}
		return cliGenerator{name: name, command: profile.Command}, nil

	case TypeOpenAICompat:
		key, err := apiKey(name, profile)
		if err != nil {
			return nil, err
		}
		if profile.BaseURL == "" {
			return nil, &ProviderError{
				Provider: name,
				Message:  fmt.Sprintf("ai provider %q has no base_url", name),
				Hint:     fmt.Sprintf("set base_url in [ai.providers.%s] to the API root, for example base_url = \"https://api.example.com/v1\"", name),
			}
		}
		return openAIGenerator{
			name:           name,
			baseURL:        profile.BaseURL,
			model:          profile.Model,
			apiKey:         key,
			maxTokensParam: profile.MaxTokensParam,
		}, nil

	case TypeAnthropic:
		key, err := apiKey(name, profile)
		if err != nil {
			return nil, err
		}
		return anthropicGenerator{name: name, baseURL: profile.BaseURL, model: profile.Model, apiKey: key}, nil

	default:
		return nil, &ProviderError{
			Provider: name,
			Message:  fmt.Sprintf("ai provider %q has no type", name),
			Hint:     fmt.Sprintf("set type in [ai.providers.%s] to anthropic, openai-compat, or cli", name),
		}
	}
}

// ProfileOpenAI is the built-in profile name for OpenAI itself, as distinct
// from the openai-compat type that also serves Ollama, Groq and OpenRouter.
const ProfileOpenAI = "openai"

const openAIBaseURL = "https://api.openai.com/v1"

// profileFor resolves a provider name to a profile. Built-in profiles exist for
// the two first-party vendors so the shipped default resolves without a config
// block; every other name must be declared.
func profileFor(cfg appconfig.Config, name string, kind string) (appconfig.ProviderConfig, bool) {
	if profile, ok := cfg.AI.Providers[name]; ok {
		return profile, true
	}

	model := cfg.AI.CommitModel
	if kind == KindPRBody {
		model = cfg.AI.PRModel
	}
	if model == "" {
		model = cfg.AI.Model
	}

	switch name {
	case ProfileOpenAI:
		return appconfig.ProviderConfig{
			Type:      TypeOpenAICompat,
			BaseURL:   openAIBaseURL,
			APIKeyEnv: "OPENAI_API_KEY",
			Model:     model,
		}, true
	case TypeAnthropic:
		return appconfig.ProviderConfig{
			Type:      TypeAnthropic,
			APIKeyEnv: "ANTHROPIC_API_KEY",
			Model:     model,
		}, true
	}

	return appconfig.ProviderConfig{}, false
}

func apiKey(name string, profile appconfig.ProviderConfig) (string, error) {
	if profile.APIKeyEnv == "" {
		return "", &ProviderError{
			Provider: name,
			Message:  fmt.Sprintf("ai provider %q has no api_key_env", name),
			Hint:     fmt.Sprintf("set api_key_env in [ai.providers.%s] to the name of an environment variable holding the key; keys are never read from config files", name),
		}
	}

	key := os.Getenv(profile.APIKeyEnv)
	if key == "" {
		return "", &ProviderError{
			Provider: name,
			Message:  fmt.Sprintf("environment variable %s is not set", profile.APIKeyEnv),
			Hint:     fmt.Sprintf("export %s with the API key for ai provider %q, or point api_key_env at a variable that is set", profile.APIKeyEnv, name),
		}
	}
	return key, nil
}

func unknownProviderHint(name string, profiles map[string]appconfig.ProviderConfig) string {
	if len(profiles) == 0 {
		return fmt.Sprintf("define the profile as [ai.providers.%s], or point [ai].provider at one that exists", name)
	}

	declared := make([]string, 0, len(profiles))
	for profile := range profiles {
		declared = append(declared, profile)
	}
	sort.Strings(declared)

	return fmt.Sprintf("define [ai.providers.%s], or set [ai].provider to one of: %s", name, strings.Join(declared, ", "))
}
