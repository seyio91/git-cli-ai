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
	// Type is the branch-name segment this message belongs under. Branch
	// naming asks the style rather than parsing the message itself, so the
	// three grammars cannot drift from the names derived off them.
	Type(message string) string
	// Rules states the same contract Validate enforces, in the form a
	// generator is prompted with. It is fixed per style so it can head a
	// prompt as a stable, cacheable prefix.
	Rules() string
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

func (conventionalStyle) Type(message string) string {
	return conventional.Type(message)
}

func (conventionalStyle) Rules() string {
	return `Style: conventional-commits
The header is a single line of the form "type(scope): subject".
- type is lowercase letters, digits and hyphens, starting with a letter
- (scope) is optional and contains only letters, digits, '.', '_', '/' or '-'
- append '!' immediately before the colon to mark a breaking change
- the colon is followed by exactly one space and a non-empty subject
An optional body may follow, separated from the header by one blank line.` + sharedRules
}

// sharedRules holds the parts every style states identically, so the three
// blocks cannot drift apart on the things that are not style-specific.
const sharedRules = `
Write the subject in the imperative mood and do not end it with a period.`

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
