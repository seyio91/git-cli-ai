// Package style validates a commit message against the configured commit
// style. Every style shares one rule: the header is validated strictly and an
// optional body after a mandatory blank line is opaque, never rejected on
// content.
package style

import (
	"fmt"
	"strings"

	"github.com/seyio91/git-cli-ai/internal/conventional"
)

const (
	Conventional = "conventional-commits"
	Gitmoji      = "gitmoji"
	Freeform     = "freeform-with-rules"
)

type Result struct {
	Valid  bool
	Reason string
}

type Validator interface {
	Validate(message string) Result
}

// For returns the validator for a resolved commit.style value. Config already
// rejects values outside the permitted set, so an error here means the two
// closed sets have drifted apart.
func For(name string) (Validator, error) {
	switch name {
	case Conventional:
		return conventionalStyle{}, nil
	case Gitmoji:
		return gitmojiStyle{}, nil
	case Freeform:
		return freeformStyle{}, nil
	default:
		return nil, fmt.Errorf("unsupported commit style %q", name)
	}
}

type conventionalStyle struct{}

func (conventionalStyle) Validate(message string) Result {
	result := conventional.Validate(message)
	return Result{Valid: result.Valid, Reason: result.Reason}
}

// splitHeader separates the header from an optional body and enforces the rule
// shared by every style: a body must be preceded by a blank line.
func splitHeader(message string) (string, Result) {
	if message == "" {
		return "", Result{Reason: "message is empty"}
	}

	header, rest, hasRest := strings.Cut(message, "\n")
	header = strings.TrimSuffix(header, "\r")

	if hasRest && strings.TrimRight(rest, "\r\n") != "" {
		firstLine, _, _ := strings.Cut(rest, "\n")
		if strings.TrimSuffix(firstLine, "\r") != "" {
			return "", Result{Reason: "body must be separated from the header by a blank line"}
		}
	}

	return header, Result{Valid: true}
}

func invalid(reason string, expected string) Result {
	return Result{Reason: fmt.Sprintf("%s; expected %s", reason, expected)}
}
