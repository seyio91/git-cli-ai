package style

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/seyio91/git-cli-ai/internal/conventional"
)

const gitmojiExpected = ":shortcode: subject or <emoji> subject"

var shortcodePattern = regexp.MustCompile(`^:[a-z0-9_+-]+:$`)

// conventionalPrefixPattern detects an optional Conventional Commits prefix
// after the emoji token, so `:sparkles: feat(api): add x` is validated by the
// conventional rules rather than by a second, drifting copy of them.
var conventionalPrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\([^()]*\))?!?: `)

type gitmojiStyle struct{}

func (gitmojiStyle) Validate(message string) Result {
	header, result := splitHeader(message)
	if !result.Valid {
		return result
	}

	token, subject, ok := strings.Cut(header, " ")
	if !ok {
		return invalid("header must be an emoji token followed by a subject", gitmojiExpected)
	}
	if !isEmojiToken(token) {
		return invalid("header must start with a :shortcode: or an emoji", gitmojiExpected)
	}
	if strings.TrimSpace(subject) == "" {
		return invalid("subject is empty", gitmojiExpected)
	}
	if strings.HasPrefix(subject, " ") {
		return invalid("subject must not start with extra whitespace", gitmojiExpected)
	}

	if conventionalPrefixPattern.MatchString(subject) {
		if r := conventional.Validate(subject); !r.Valid {
			return Result{Reason: r.Reason}
		}
	}

	return Result{Valid: true}
}

func (gitmojiStyle) Rules() string {
	return `Style: gitmoji
The header is a single line beginning with an emoji token, then one space, then
a non-empty subject. The token is either a :shortcode: (lowercase letters,
digits, '_', '+' or '-' between colons) or a literal emoji character.
If the subject itself carries a "type(scope): " prefix, that prefix must satisfy
the Conventional Commits rules.
An optional body may follow, separated from the header by one blank line.` + sharedRules
}

// isEmojiToken accepts a :shortcode: or a run of non-ASCII runes. The latter is
// deliberately loose: emoji are multi-rune sequences (skin tones, ZWJ joins,
// variation selectors) and enumerating them would date badly, so any token that
// is entirely non-ASCII counts.
func isEmojiToken(token string) bool {
	if token == "" {
		return false
	}
	if shortcodePattern.MatchString(token) {
		return true
	}
	for _, r := range token {
		if r <= unicode.MaxASCII {
			return false
		}
	}
	return true
}
