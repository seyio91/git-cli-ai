// Package ai turns a staged change into a commit message. The request handed
// to a provider is deliberately provider-neutral: the same GenRequest is the
// payload `--context-only` emits, so what an agent inspects is exactly what a
// provider would have received.
package ai

import (
	"context"
	"strings"
)

// Kind selects what is being written. It only changes the preamble and which
// model a built-in profile resolves to: the request shape, the prompt assembly
// and every provider stay shared.
const (
	KindCommit = "commit"
	KindPRBody = "pr-body"
)

type GenRequest struct {
	// Kind is empty for a commit message, which is the original and default
	// request, so the --context-only payload is unchanged by its addition.
	Kind string `json:"kind,omitempty"`
	// Style is the rules block for the active commit style, or the PR
	// template for a pr-body request. It is stable across requests and is
	// rendered first so it can serve as a cacheable prompt prefix.
	Style    string         `json:"style"`
	Diff     string         `json:"diff"`
	Context  []ContextBlock `json:"context,omitempty"`
	Intent   string         `json:"intent,omitempty"`
	Feedback *Feedback      `json:"feedback,omitempty"`
}

type ContextBlock struct {
	Label   string `json:"label"`
	Content string `json:"content"`
}

// Feedback carries a rejected message and the validator's reason back to the
// generator so a retry can correct itself rather than guess again.
type Feedback struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

type GenResult struct {
	Message string `json:"message"`
}

type Generator interface {
	Generate(ctx context.Context, req GenRequest) (GenResult, error)
}

// ProviderError reports a failure to obtain a message from the configured
// provider. Hint is actionable guidance; Details carries the provider's own
// words, mirroring the git error contract.
type ProviderError struct {
	Provider string
	Message  string
	Hint     string
	Details  string
}

func (e *ProviderError) Error() string {
	return e.Message
}

const instructionsPreamble = `Write the commit message for the staged change described below.
Reply with the message itself and nothing else: no preamble, no commentary, no
code fences.`

const prBodyPreamble = `Write the pull request body for the change described below, filling in the
template that follows. Keep its headings and their order, replace every
{{placeholder}} with real content, and leave a placeholder's section empty when
nothing is known about it rather than inventing something.
Reply with the body itself and nothing else: no preamble, no commentary, no
code fences.`

// Instructions is the stable half of the prompt: it depends only on the commit
// style or the PR template, never on the change being described.
func (r GenRequest) Instructions() string {
	preamble := instructionsPreamble
	if r.Kind == KindPRBody {
		preamble = prBodyPreamble
	}
	return preamble + "\n\n" + r.Style
}

// Task is the volatile half: the intent, the context blocks, the diff, and any
// feedback from a rejected attempt.
func (r GenRequest) Task() string {
	var b strings.Builder

	if r.Intent != "" {
		b.WriteString("The author already described this change:\n\n")
		b.WriteString(r.Intent)
		b.WriteString("\n\nReformat that description to satisfy the rules above. It is the sole\n" +
			"source of meaning: do not add, drop, or correct anything based on the diff,\n" +
			"which is included only to help you choose a type and scope.\n")
	} else {
		b.WriteString("Describe what the diff below changes.\n")
	}

	for _, block := range r.Context {
		b.WriteString("\n## ")
		b.WriteString(block.Label)
		b.WriteString("\n")
		b.WriteString(block.Content)
		b.WriteString("\n")
	}

	b.WriteString("\n## diff\n")
	b.WriteString(r.Diff)
	b.WriteString("\n")

	if r.Feedback != nil {
		b.WriteString("\nYour previous attempt was rejected: ")
		b.WriteString(r.Feedback.Reason)
		b.WriteString("\n\nThe rejected message was:\n")
		b.WriteString(r.Feedback.Message)
		b.WriteString("\n\nReturn a corrected message.\n")
	}

	return b.String()
}

func (r GenRequest) Prompt() string {
	return r.Instructions() + "\n\n" + r.Task()
}
