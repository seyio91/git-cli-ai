package ai

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const anthropicReply = `{"content":[{"type":"text","text":"feat(api): add the thing"}]}`

func TestAnthropicSendsTheDocumentedRequestShape(t *testing.T) {
	server, got := newTestServer(t, http.StatusOK, anthropicReply)
	gen := anthropicGenerator{name: "test", baseURL: server.URL, model: "claude-haiku-4-5", apiKey: "sk-ant-test"}

	result, err := gen.Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Message != "feat(api): add the thing" {
		t.Fatalf("message = %q", result.Message)
	}

	if got.path != "/v1/messages" {
		t.Fatalf("path = %s, want /v1/messages", got.path)
	}
	// Header names are canonicalised by net/http; the wire names are
	// case-insensitive, so x-api-key and X-Api-Key are the same header.
	if key := got.header.Get("X-Api-Key"); key != "sk-ant-test" {
		t.Fatalf("x-api-key = %q", key)
	}
	if v := got.header.Get("Anthropic-Version"); v != anthropicVersion {
		t.Fatalf("anthropic-version = %q, want %q", v, anthropicVersion)
	}
	if got.header.Get("Authorization") != "" {
		t.Fatal("the key must go in x-api-key, not Authorization")
	}

	if got.body["model"] != "claude-haiku-4-5" {
		t.Fatalf("model = %v", got.body["model"])
	}
	if got.body["max_tokens"] == nil {
		t.Fatal("max_tokens was not sent")
	}

	// system is a list of content blocks, which is the form that can later
	// carry cache_control.
	system, ok := got.body["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("system = %#v, want a one-block list", got.body["system"])
	}
	block, _ := system[0].(map[string]any)
	if block["type"] != "text" {
		t.Fatalf("system block type = %v, want text", block["type"])
	}
	if text, _ := block["text"].(string); !strings.Contains(text, "conventional-commits") {
		t.Fatalf("system block does not carry the style rules: %q", text)
	}
	if text, _ := block["text"].(string); strings.Contains(text, "diff --git") {
		t.Fatal("the diff must not appear in the system block")
	}

	messages, ok := got.body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v, want a single user turn", got.body["messages"])
	}
	user, _ := messages[0].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("role = %v, want user", user["role"])
	}
	content, ok := user["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("user content = %#v, want a one-block list", user["content"])
	}
	if userBlock, _ := content[0].(map[string]any); !strings.Contains(userBlock["text"].(string), "diff --git") {
		t.Fatal("user turn does not carry the diff")
	}
}

func TestAnthropicURLTolerates(t *testing.T) {
	for _, tc := range []struct{ name, base, want string }{
		{"unset", "", anthropicBaseURL + "/v1/messages"},
		{"root", "https://api.example.com", "https://api.example.com/v1/messages"},
		{"trailing slash", "https://api.example.com/", "https://api.example.com/v1/messages"},
		{"endpoint named", "https://api.example.com/v1/messages", "https://api.example.com/v1/messages"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := anthropicMessagesURL(tc.base); got != tc.want {
				t.Fatalf("anthropicMessagesURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

// A response may carry several blocks; only text contributes to the message,
// and the pieces are joined rather than the first one winning.
func TestAnthropicJoinsTextBlocksAndIgnoresOthers(t *testing.T) {
	body := `{"content":[
		{"type":"thinking","text":"ignore me"},
		{"type":"text","text":"feat(api): add "},
		{"type":"text","text":"the thing"}
	]}`
	server, _ := newTestServer(t, http.StatusOK, body)
	gen := anthropicGenerator{name: "test", baseURL: server.URL, model: "claude-haiku-4-5", apiKey: "sk-ant-test"}

	result, err := gen.Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Message != "feat(api): add the thing" {
		t.Fatalf("message = %q, want the text blocks joined with non-text dropped", result.Message)
	}
}

func TestAnthropicRejectsEmptyContent(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no blocks", `{"content":[]}`},
		{"no text blocks", `{"content":[{"type":"thinking","text":"only thinking"}]}`},
		{"blank text", `{"content":[{"type":"text","text":"  "}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTestServer(t, http.StatusOK, tc.body)
			gen := anthropicGenerator{name: "test", baseURL: server.URL, model: "claude-haiku-4-5", apiKey: "sk-ant-test"}

			_, err := gen.Generate(context.Background(), testRequest())
			if err == nil {
				t.Fatal("expected an error rather than an empty message")
			}
			if pe := providerError(t, err); !strings.Contains(pe.Message, "empty message") {
				t.Fatalf("message = %q", pe.Message)
			}
		})
	}
}

func TestAnthropicRequiresModelBeforeCallingOut(t *testing.T) {
	server, got := newTestServer(t, http.StatusOK, anthropicReply)
	gen := anthropicGenerator{name: "test", baseURL: server.URL, apiKey: "sk-ant-test"}

	_, err := gen.Generate(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error when no model is configured")
	}
	if got.method != "" {
		t.Fatal("a request was sent despite the missing model")
	}
}

// The key is a secret: it must never reach an error payload, which is printed
// on every failure and pasted into bug reports.
func TestAnthropicNeverEchoesTheKeyInErrors(t *testing.T) {
	const key = "sk-ant-SUPERSECRET"
	server, _ := newTestServer(t, http.StatusUnauthorized, `{"error":{"message":"invalid x-api-key"}}`)
	gen := anthropicGenerator{name: "test", baseURL: server.URL, model: "claude-haiku-4-5", apiKey: key}

	_, err := gen.Generate(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error for HTTP 401")
	}

	pe := providerError(t, err)
	combined := pe.Message + pe.Hint + pe.Details
	if strings.Contains(combined, key) {
		t.Fatalf("the api key was echoed into the error payload: %q", combined)
	}
}
