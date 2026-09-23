package conventional

import (
	"fmt"
	"regexp"
	"strings"
)

var typePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var scopePattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

type Result struct {
	Valid  bool
	Reason string
	// TypeNotAllowed marks the one failure that is about vocabulary rather
	// than grammar: the header is a well-formed conventional commit and its
	// type is simply not on the configured list. The command layer routes that
	// differently from every other rejection — a message that already declares
	// a type is a statement, not intent to be rewritten — and there is no way
	// to tell the two apart from Reason without matching on prose.
	TypeNotAllowed bool
}

// ValidType reports whether s is well formed as a conventional commit type. It
// is exported so config can reject a bad entry in commit.types at load, naming
// the file that set it, rather than letting it sit in a vocabulary that can
// never match anything.
func ValidType(s string) bool {
	return typePattern.MatchString(s)
}

// Validate checks a commit message against Conventional Commits. The header is
// validated strictly; an optional body after a mandatory blank line is opaque
// and never rejected on content.
//
// types is the closed vocabulary the type must belong to. Nil or empty means
// any well-formed type is accepted, which is what this package did before the
// parameter existed and what every repo that configures nothing still gets.
func Validate(message string, types []string) Result {
	if message == "" {
		return invalid("message is empty")
	}

	header, rest, hasRest := strings.Cut(message, "\n")
	header = strings.TrimSuffix(header, "\r")

	if result := validateHeader(header, types); !result.Valid {
		return result
	}

	if hasRest && strings.TrimRight(rest, "\r\n") != "" {
		firstLine, _, _ := strings.Cut(rest, "\n")
		if strings.TrimSuffix(firstLine, "\r") != "" {
			return Result{
				Valid:  false,
				Reason: "body must be separated from the header by a blank line",
			}
		}
	}

	return Result{Valid: true}
}

func validateHeader(message string, types []string) Result {
	colon := strings.Index(message, ":")
	if colon == -1 {
		return invalid("missing ': ' separator")
	}
	if colon+1 >= len(message) || message[colon:colon+2] != ": " {
		return invalid("separator must be ': '")
	}

	prefix := message[:colon]
	subject := message[colon+2:]
	if strings.TrimSpace(subject) == "" {
		return invalid("subject is empty")
	}
	if strings.HasPrefix(subject, " ") {
		return invalid("subject must not start with extra whitespace")
	}

	if strings.HasSuffix(prefix, "!") {
		prefix = strings.TrimSuffix(prefix, "!")
		if prefix == "" {
			return invalid("type is empty")
		}
	}

	typ, scope, hasScope, ok := splitPrefix(prefix)
	if !ok {
		return invalid("scope must be written as '(scope)' before the colon")
	}
	if !typePattern.MatchString(typ) {
		return invalid("type must start with a lowercase letter and contain only lowercase letters, digits, or hyphens")
	}
	// Checked after the grammar so that a malformed type is reported as
	// malformed rather than as "not in the list", which would send someone to
	// edit their config over a typo.
	if !allowedType(typ, types) {
		return Result{
			Reason:         fmt.Sprintf("type %q is not one of the allowed types: %s", typ, strings.Join(types, ", ")),
			TypeNotAllowed: true,
		}
	}
	if hasScope {
		if scope == "" {
			return invalid("scope is empty")
		}
		if !scopePattern.MatchString(scope) {
			return invalid("scope must contain only letters, digits, '.', '_', '/', or '-'")
		}
	}

	return Result{Valid: true}
}

// allowedType reports whether typ is permitted. An empty vocabulary permits
// everything, so the open behaviour falls out of the same code path rather
// than being a branch somewhere above it.
func allowedType(typ string, types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, allowed := range types {
		if typ == allowed {
			return true
		}
	}
	return false
}

func splitPrefix(prefix string) (string, string, bool, bool) {
	open := strings.Index(prefix, "(")
	if open == -1 {
		if strings.Contains(prefix, ")") {
			return "", "", false, false
		}
		return prefix, "", false, true
	}

	if !strings.HasSuffix(prefix, ")") {
		return "", "", false, false
	}
	if strings.Count(prefix, "(") != 1 || strings.Count(prefix, ")") != 1 {
		return "", "", false, false
	}

	return prefix[:open], prefix[open+1 : len(prefix)-1], true, true
}

func invalid(reason string) Result {
	return Result{
		Valid:  false,
		Reason: fmt.Sprintf("%s; expected type(scope): subject", reason),
	}
}

// Type returns the header's type, or "" when the message is not conventional.
// Branch naming needs only this much of the header, which is why the body stays
// opaque.
//
// Deliberately vocabulary-blind: this is only ever called on a message that has
// already been accepted, so a list here could never change the answer.
func Type(message string) string {
	if !Validate(message, nil).Valid {
		return ""
	}

	header, _, _ := strings.Cut(message, "\n")
	prefix, _, ok := strings.Cut(strings.TrimSuffix(header, "\r"), ":")
	if !ok {
		return ""
	}

	name, _, _, ok := splitPrefix(strings.TrimSuffix(prefix, "!"))
	if !ok {
		return ""
	}
	return name
}
