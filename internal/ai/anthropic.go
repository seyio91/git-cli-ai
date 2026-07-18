package ai

import (
	"context"
	"net/http"
	"strings"
)

const (
	anthropicBaseURL = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
)

type anthropicGenerator struct {
	name    string
	baseURL string
	model   string
	apiKey  string
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    []anthropicText    `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content []anthropicText `json:"content"`
}

type anthropicText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicText `json:"content"`
}

func (g anthropicGenerator) Generate(ctx context.Context, req GenRequest) (GenResult, error) {
	if err := requireModel(g.name, g.model); err != nil {
		return GenResult{}, err
	}

	header := http.Header{}
	header.Set("X-Api-Key", g.apiKey)
	header.Set("Anthropic-Version", anthropicVersion)

	// The style rules go in the system block and the change in the user turn,
	// so the stable prefix stays byte-identical across requests.
	body := anthropicRequest{
		Model:     g.model,
		MaxTokens: maxOutputTokens,
		System:    []anthropicText{{Type: "text", Text: req.Instructions()}},
		Messages: []anthropicMessage{
			{Role: "user", Content: []anthropicText{{Type: "text", Text: req.Task()}}},
		},
	}

	var response anthropicResponse
	if err := postJSON(ctx, g.name, anthropicMessagesURL(g.baseURL), header, body, &response); err != nil {
		return GenResult{}, err
	}

	var text strings.Builder
	for _, block := range response.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	message := strings.TrimSpace(text.String())
	if message == "" {
		return GenResult{}, emptyMessageError(g.name)
	}

	return GenResult{Message: message}, nil
}

func anthropicMessagesURL(baseURL string) string {
	if baseURL == "" {
		baseURL = anthropicBaseURL
	}
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1/messages") {
		return trimmed
	}
	return trimmed + "/v1/messages"
}
