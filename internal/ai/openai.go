package ai

import (
	"context"
	"net/http"
	"strings"
)

// openAIGenerator drives any chat-completions endpoint. Only base_url,
// api_key_env and model distinguish one such provider from another, so they are
// all one code path.
type openAIGenerator struct {
	name    string
	baseURL string
	model   string
	apiKey  string
}

type openAIRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
}

func (g openAIGenerator) Generate(ctx context.Context, req GenRequest) (GenResult, error) {
	if err := requireModel(g.name, g.model); err != nil {
		return GenResult{}, err
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+g.apiKey)

	body := openAIRequest{
		Model:     g.model,
		MaxTokens: maxOutputTokens,
		Messages: []openAIMessage{
			{Role: "system", Content: req.Instructions()},
			{Role: "user", Content: req.Task()},
		},
	}

	var response openAIResponse
	if err := postJSON(ctx, g.name, chatCompletionsURL(g.baseURL), header, body, &response); err != nil {
		return GenResult{}, err
	}

	if len(response.Choices) == 0 {
		return GenResult{}, emptyMessageError(g.name)
	}
	message := strings.TrimSpace(response.Choices[0].Message.Content)
	if message == "" {
		return GenResult{}, emptyMessageError(g.name)
	}

	return GenResult{Message: message}, nil
}

// chatCompletionsURL treats base_url as the API root, but tolerates a base_url
// that already names the endpoint.
func chatCompletionsURL(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	return trimmed + "/chat/completions"
}
