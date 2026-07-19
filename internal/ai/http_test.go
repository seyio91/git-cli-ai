package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The HTTP providers are the part of this package no acceptance test can
// reach: the suite drives generation through the `cli` provider so it never
// touches the network. These tests stand up an in-process server instead, so
// the request shape, the status mapping, and the response decoding are all
// exercised without a key and without leaving the machine.

// captured records what a generator actually sent.
type captured struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

// newTestServer returns a server that records one request and replies with
// status and body. The returned pointer is populated by the time Generate
// returns.
func newTestServer(t *testing.T, status int, body string) (*httptest.Server, *captured) {
	t.Helper()

	got := &captured{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		got.method = r.Method
		got.path = r.URL.Path
		got.header = r.Header.Clone()
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &got.body); err != nil {
				t.Errorf("request body is not JSON (%v): %q", err, raw)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	return server, got
}

func testRequest() GenRequest {
	return GenRequest{
		Style: "Style: conventional-commits\nThe header is a single line.",
		Diff:  "diff --git a/a.txt b/a.txt\n+hello\n",
	}
}

func providerError(t *testing.T, err error) *ProviderError {
	t.Helper()

	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not a *ProviderError: %#v", err)
	}
	return pe
}

// A successful response larger than maxErrorBody must still decode. The error
// path quotes at most maxErrorBody bytes; reading through that same limit
// before decoding would fail to parse any real completion, and no test that
// stops at the transport layer would catch it.
func TestPostJSONDecodesResponseLargerThanTheErrorQuota(t *testing.T) {
	long := strings.Repeat("x", maxErrorBody*4)
	payload, err := json.Marshal(map[string]any{"value": long})
	if err != nil {
		t.Fatal(err)
	}

	server, _ := newTestServer(t, http.StatusOK, string(payload))

	var out struct {
		Value string `json:"value"`
	}
	if err := postJSON(context.Background(), "test", server.URL, http.Header{}, map[string]any{}, &out); err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if len(out.Value) != len(long) {
		t.Fatalf("decoded %d bytes, want %d — the response was truncated before decoding", len(out.Value), len(long))
	}
}

// An oversized error body is quoted, not dumped whole.
func TestPostJSONTruncatesQuotedErrorBody(t *testing.T) {
	server, _ := newTestServer(t, http.StatusInternalServerError, strings.Repeat("y", maxErrorBody*4))

	err := postJSON(context.Background(), "test", server.URL, http.Header{}, map[string]any{}, &struct{}{})
	if err == nil {
		t.Fatal("expected an error for HTTP 500")
	}

	pe := providerError(t, err)
	if len(pe.Details) > maxErrorBody+64 {
		t.Fatalf("details is %d bytes, want it bounded near maxErrorBody (%d)", len(pe.Details), maxErrorBody)
	}
	if !strings.Contains(pe.Details, "truncated") {
		t.Fatalf("a truncated body should say so: %q", pe.Details[:64])
	}
}

func TestPostJSONMapsStatusToActionableHint(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "api_key_env"},
		{http.StatusForbidden, "api_key_env"},
		{http.StatusNotFound, "base_url"},
		{http.StatusTooManyRequests, "rate limiting"},
		{http.StatusBadGateway, "server-side"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server, _ := newTestServer(t, tc.status, `{"error":"nope"}`)

			err := postJSON(context.Background(), "test", server.URL, http.Header{}, map[string]any{}, &struct{}{})
			if err == nil {
				t.Fatalf("expected an error for HTTP %d", tc.status)
			}

			pe := providerError(t, err)
			if !strings.Contains(pe.Hint, tc.want) {
				t.Fatalf("hint for %d = %q, want it to mention %q", tc.status, pe.Hint, tc.want)
			}
			if pe.Hint == pe.Details {
				t.Fatal("hint must be guidance, not a copy of the provider's own words")
			}
		})
	}
}

// A body that is not the expected shape is a structured error, not a panic and
// not a silently zero-valued result.
func TestPostJSONReportsUndecodableBody(t *testing.T) {
	server, _ := newTestServer(t, http.StatusOK, "<html>not json</html>")

	err := postJSON(context.Background(), "test", server.URL, http.Header{}, map[string]any{}, &struct{}{})
	if err == nil {
		t.Fatal("expected an error for a non-JSON 200")
	}
	if pe := providerError(t, err); !strings.Contains(pe.Hint, "base_url") {
		t.Fatalf("hint = %q, want it to point at the configured endpoint", pe.Hint)
	}
}

func TestPostJSONReportsUnreachableHost(t *testing.T) {
	// Port 1 on loopback: closed, so this fails fast without leaving the machine.
	err := postJSON(context.Background(), "test", "http://127.0.0.1:1", http.Header{}, map[string]any{}, &struct{}{})
	if err == nil {
		t.Fatal("expected an error for an unreachable host")
	}
	if pe := providerError(t, err); !strings.Contains(pe.Message, "could not be reached") {
		t.Fatalf("message = %q, want it to say the provider was unreachable", pe.Message)
	}
}

func TestPostJSONRejectsUnusableBaseURL(t *testing.T) {
	err := postJSON(context.Background(), "test", "://not a url", http.Header{}, map[string]any{}, &struct{}{})
	if err == nil {
		t.Fatal("expected an error for a malformed base_url")
	}
	if pe := providerError(t, err); !strings.Contains(pe.Hint, "absolute http or https URL") {
		t.Fatalf("hint = %q, want it to describe a valid base_url", pe.Hint)
	}
}

// A cancelled context must abort the call rather than run to the client
// timeout: the CLI has to stay interruptible.
func TestPostJSONHonoursContextCancellation(t *testing.T) {
	server, _ := newTestServer(t, http.StatusOK, `{}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := postJSON(ctx, "test", server.URL, http.Header{}, map[string]any{}, &struct{}{})
	if err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}
