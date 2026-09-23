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
	// TypeNotAllowed marks a message that is well formed in its style and
	// fails only on the configured type vocabulary. The command layer treats
	// that as a statement to refuse rather than as intent to render, so the
	// two kinds of invalid have to be distinguishable. Styles with no notion
	// of a type never set it.
	TypeNotAllowed bool
}

type Validator interface {
	Validate(message string) Result
	// Type is the branch-name segment this message belongs under. Branch
	// naming asks the style rather than parsing the message itself, so the
	// three grammars cannot drift from the names derived off them.
	Type(message string) string
	// Subject is the description a branch slug is built from: the header with
	// the style's own prefix (type, emoji) removed. Kept on the validator for
	// the same reason as Type — the grammar owns its own parsing.
	Subject(message string) string
	// Rules states the same contract Validate enforces, in the form a
	// generator is prompted with. It is fixed for a given style and
	// configuration, so it still heads a prompt as a stable, cacheable prefix
	// — but it is no longer identical across repositories, since a configured
	// commit.types is part of the contract and has to be stated.
	Rules() string
}

// For returns the validator for a resolved commit.style value. Config already
// rejects values outside the permitted set, so an error here means the two
// closed sets have drifted apart.
//
// types is the configured commit.types. Nil or empty is the open vocabulary,
// so a caller with nothing configured passes the config value through
// unexamined rather than branching on it.
func For(name string, types []string) (Validator, error) {
	switch name {
	case Conventional:
		return conventionalStyle{types: types}, nil
	case Gitmoji:
		return gitmojiStyle{types: types}, nil
	case Freeform:
		return freeformStyle{}, nil
	default:
		return nil, fmt.Errorf("unsupported commit style %q", name)
	}
}

type conventionalStyle struct{ types []string }

func (s conventionalStyle) Validate(message string) Result {
	result := conventional.Validate(message, s.types)
	return Result{Valid: result.Valid, Reason: result.Reason, TypeNotAllowed: result.TypeNotAllowed}
}

func (conventionalStyle) Type(message string) string {
	// Vocabulary-blind for the same reason conventional.Type is: this only
	// runs on a message that already validated.
	return conventional.Type(message)
}

func (s conventionalStyle) Subject(message string) string {
	return afterPrefix(headerOf(message))
}

func (s conventionalStyle) Rules() string {
	return `Style: conventional-commits
The header is a single line of the form "type(scope): subject".
- type is lowercase letters, digits and hyphens, starting with a letter
- (scope) is optional and contains only letters, digits, '.', '_', '/' or '-'
- append '!' immediately before the colon to mark a breaking change
- the colon is followed by exactly one space and a non-empty subject
An optional body may follow, separated from the header by one blank line.` +
		vocabularyRule(s.types) + sharedRules
}

// vocabularyRule states a configured commit.types to the generator. The
// validator will reject anything outside the list either way, so this is what
// turns a second round trip into a first-attempt success.
func vocabularyRule(types []string) string {
	if len(types) == 0 {
		return ""
	}
	return "\nThe type must be exactly one of: " + strings.Join(types, ", ") + "."
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

// headerOf is the first line of a message, without a trailing CR.
func headerOf(message string) string {
	header, _, _ := strings.Cut(message, "\n")
	return strings.TrimSuffix(header, "\r")
}

// afterPrefix drops a leading "type(scope): " style prefix, returning the
// description. A header with no such prefix is its own subject.
func afterPrefix(header string) string {
	if _, desc, ok := strings.Cut(header, ": "); ok {
		return desc
	}
	return header
}
