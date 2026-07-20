package style

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxHeaderLength keeps the header readable in `git log --oneline` and in PR
// list views.
const maxHeaderLength = 72

const freeformExpected = "a subject of at most 72 characters with no trailing period"

type freeformStyle struct{}

func (freeformStyle) Rules() string {
	return `Style: freeform-with-rules
The header is a single line of at most 72 characters, non-empty, with no leading
whitespace and no trailing period. There is no required prefix.
An optional body may follow, separated from the header by one blank line.` + sharedRules
}

func (freeformStyle) Validate(message string) Result {
	header, result := splitHeader(message)
	if !result.Valid {
		return result
	}

	if strings.TrimSpace(header) == "" {
		return invalid("header is empty", freeformExpected)
	}
	if strings.HasPrefix(header, " ") {
		return invalid("header must not start with extra whitespace", freeformExpected)
	}
	if count := utf8.RuneCountInString(header); count > maxHeaderLength {
		return invalid(fmt.Sprintf("header is %d characters", count), freeformExpected)
	}
	if strings.HasSuffix(header, ".") {
		return invalid("header ends with a period", freeformExpected)
	}

	return Result{Valid: true}
}

// Type is empty: freeform messages carry no type, so the branch name comes from
// the subject alone.
func (freeformStyle) Type(string) string { return "" }
