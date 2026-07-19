package ai

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const openAIReply = `{"choices":[{"message":{"role":"assistant","content":"feat(api): add the thing"}}]}`

func TestOpenAISendsTheDocumentedRequestShape(t *testing.T) {
	server, got := newTestServer(t, http.StatusOK, openAIReply)
	gen := openAIGenerator{name: "test", baseURL: server.URL, model: "test-model", apiKey: "sk-test"}

	result, err := gen.Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Message != "feat(api): add the thing" {
		t.Fatalf("message = %q", result.Message)
	}

	if got.method != http.MethodPost {
		t.Fatalf("method = %s, want POST", got.method)
	}
	if got.path != "/chat/completions" {
		t.Fatalf("path = %s, want /chat/completions", got.path)
	}
	if auth := got.header.Get("Authorization"); auth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want a bearer token", auth)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}

	if got.body["model"] != "test-model" {
		t.Fatalf("model = %v, want the configured model", got.body["model"])
	}
	if got.body["max_tokens"] == nil {
		t.Fatal("max_tokens was not sent")
	}

	messages, ok := got.body["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v, want a system turn and a user turn", got.body["messages"])
	}
	system, _ := messages[0].(map[string]any)
	user, _ := messages[1].(map[string]any)
	if system["role"] != "system" || user["role"] != "user" {
		t.Fatalf("roles = %v/%v, want system then user", system["role"], user["role"])
	}

	// The stable rules go in the system turn and the change in the user turn.
	// If the diff leaks into the system turn the prefix changes per request.
	if content, _ := system["content"].(string); !strings.Contains(content, "conventional-commits") {
		t.Fatalf("system turn does not carry the style rules: %q", content)
	}
	if content, _ := system["content"].(string); strings.Contains(content, "diff --git") {
		t.Fatal("the diff must not appear in the system turn")
	}
	if content, _ := user["content"].(string); !strings.Contains(content, "diff --git") {
		t.Fatal("user turn does not carry the diff")
	}
}

// base_url is documented as the API root, but a base_url that already names the
// endpoint must not produce /chat/completions/chat/completions.
func TestOpenAIURLTolerates(t *testing.T) {
	for _, tc := range []struct{ name, base, want string }{
		{"root", "https://api.example.com/v1", "https://api.example.com/v1/chat/completions"},
		{"trailing slash", "https://api.example.com/v1/", "https://api.example.com/v1/chat/completions"},
		{"endpoint named", "https://api.example.com/v1/chat/completions", "https://api.example.com/v1/chat/completions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatCompletionsURL(tc.base); got != tc.want {
				t.Fatalf("chatCompletionsURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

func TestOpenAIRejectsEmptyCompletions(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no choices", `{"choices":[]}`},
		{"blank content", `{"choices":[{"message":{"role":"assistant","content":"   "}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTestServer(t, http.StatusOK, tc.body)
			gen := openAIGenerator{name: "test", baseURL: server.URL, model: "test-model", apiKey: "sk-test"}

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

// A missing model is caught before the request is built, so a misconfiguration
// costs nothing and reports the key to fix.
func TestOpenAIRequiresModelBeforeCallingOut(t *testing.T) {
	server, got := newTestServer(t, http.StatusOK, openAIReply)
	gen := openAIGenerator{name: "test", baseURL: server.URL, apiKey: "sk-test"}

	_, err := gen.Generate(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error when no model is configured")
	}
	if pe := providerError(t, err); !strings.Contains(pe.Hint, "model") {
		t.Fatalf("hint = %q, want it to name the model key", pe.Hint)
	}
	if got.method != "" {
		t.Fatal("a request was sent despite the missing model")
	}
}
