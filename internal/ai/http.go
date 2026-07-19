package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	requestTimeout = 60 * time.Second

	// maxCommitTokens is sized for a header plus a short body. A commit
	// message that needs more than this is not one worth committing.
	maxCommitTokens = 1024

	// maxPRBodyTokens is larger because a PR body is a multi-section document,
	// not a subject line. Sharing the commit ceiling truncated real bodies.
	maxPRBodyTokens = 4096

	// maxErrorBody bounds how much of a provider's error response is quoted
	// back, so a stray HTML page cannot become the whole error payload. It
	// applies to the quoted text only: truncating before decoding would break
	// any successful response larger than it.
	maxErrorBody = 2048

	// maxResponseBody is the read ceiling, so a runaway or hostile endpoint
	// cannot stream unbounded data into memory.
	maxResponseBody = 4 << 20
)

func httpClient() *http.Client {
	return &http.Client{Timeout: requestTimeout}
}

// postJSON sends body to url and decodes a 2xx response into out. A non-2xx
// response is a provider error carrying the response body as details.
func postJSON(ctx context.Context, provider string, url string, header http.Header, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return &ProviderError{Provider: provider, Message: fmt.Sprintf("ai provider %q: could not encode the request", provider), Details: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return &ProviderError{
			Provider: provider,
			Message:  fmt.Sprintf("ai provider %q has an unusable base_url", provider),
			Hint:     fmt.Sprintf("check base_url in [ai.providers.%s]; it must be an absolute http or https URL", provider),
			Details:  err.Error(),
		}
	}
	req.Header = header
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient().Do(req)
	if err != nil {
		return &ProviderError{
			Provider: provider,
			Message:  fmt.Sprintf("ai provider %q could not be reached", provider),
			Hint:     fmt.Sprintf("check base_url in [ai.providers.%s] and that the host is reachable from here", provider),
			Details:  err.Error(),
		}
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return &ProviderError{Provider: provider, Message: fmt.Sprintf("ai provider %q returned an unreadable response", provider), Details: err.Error()}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ProviderError{
			Provider: provider,
			Message:  fmt.Sprintf("ai provider %q returned HTTP %d", provider, resp.StatusCode),
			Hint:     statusHint(provider, resp.StatusCode),
			Details:  truncate(strings.TrimSpace(string(payload)), maxErrorBody),
		}
	}

	if err := json.Unmarshal(payload, out); err != nil {
		return &ProviderError{
			Provider: provider,
			Message:  fmt.Sprintf("ai provider %q returned a response this tool could not parse", provider),
			Hint:     fmt.Sprintf("check that base_url in [ai.providers.%s] points at an API compatible with the configured type", provider),
			Details:  err.Error(),
		}
	}
	return nil
}

// outputTokensFor sizes the ceiling to what is being written.
func outputTokensFor(kind string) int {
	if kind == KindPRBody {
		return maxPRBodyTokens
	}
	return maxCommitTokens
}

// truncatedError reports output that stopped at the token ceiling. Both APIs
// say so on the wire, and a truncated result is otherwise indistinguishable
// from a complete one — a commit body cut mid-sentence still passes validation,
// because body content is deliberately opaque.
func truncatedError(provider string, limit int) error {
	return &ProviderError{
		Provider: provider,
		Message:  fmt.Sprintf("ai provider %q stopped at the %d-token output limit", provider, limit),
		Hint:     "the response was cut off rather than finished; retry, shorten the change, or supply the text yourself",
	}
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "… (truncated)"
}

func statusHint(provider string, status int) string {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Sprintf("the key in the variable named by api_key_env was rejected; check that it is current and authorised for ai provider %q", provider)
	case status == http.StatusNotFound:
		return fmt.Sprintf("check base_url and model in [ai.providers.%s]; the endpoint or the model does not exist", provider)
	case status == http.StatusTooManyRequests:
		return "the provider is rate limiting; retry shortly, or supply --message to commit without a provider call"
	case status >= 500:
		return "the provider reported a server-side failure; retry shortly, or supply --message to commit without a provider call"
	}
	return fmt.Sprintf("check the request settings in [ai.providers.%s] against the provider's API documentation", provider)
}

func requireModel(provider string, model string) error {
	if model != "" {
		return nil
	}
	return &ProviderError{
		Provider: provider,
		Message:  fmt.Sprintf("ai provider %q has no model", provider),
		Hint:     fmt.Sprintf("set model in [ai.providers.%s] to a model id the provider serves", provider),
	}
}

func emptyMessageError(provider string) error {
	return &ProviderError{
		Provider: provider,
		Message:  fmt.Sprintf("ai provider %q returned an empty message", provider),
		Hint:     "the provider accepted the request but produced no text; retry, or supply --message to commit without a provider call",
	}
}
