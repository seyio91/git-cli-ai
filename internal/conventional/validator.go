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
}

// Validate checks a commit message against Conventional Commits. The header is
// validated strictly; an optional body after a mandatory blank line is opaque
// and never rejected on content.
func Validate(message string) Result {
	if message == "" {
		return invalid("message is empty")
	}

	header, rest, hasRest := strings.Cut(message, "\n")
	header = strings.TrimSuffix(header, "\r")

	if result := validateHeader(header); !result.Valid {
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

func validateHeader(message string) Result {
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
