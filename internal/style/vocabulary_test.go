package style

import (
	"strings"
	"testing"
)

var vocabulary = []string{"feat", "fix", "chore"}

func validatorFor(t *testing.T, name string, types []string) Validator {
	t.Helper()

	v, err := For(name, types)
	if err != nil {
		t.Fatalf("For(%q): %v", name, err)
	}
	return v
}

// An unset vocabulary must behave exactly as this package did before it
// existed. This is the guarantee that makes the option opt-in, and the only
// way to hold it is to assert it — `ai:` is the real type that slipped
// through and became an ai/ branch namespace.
func TestUnsetVocabularyAcceptsAnyWellFormedType(t *testing.T) {
	for _, name := range []string{Conventional, Gitmoji} {
		t.Run(name, func(t *testing.T) {
			message := "ai: discourage inflated language"
			if name == Gitmoji {
				message = ":sparkles: " + message
			}

			if result := validatorFor(t, name, nil).Validate(message); !result.Valid {
				t.Errorf("Validate(%q) rejected with %q, want accepted", message, result.Reason)
			}
		})
	}
}

// A set vocabulary rejects an unlisted type, names the allowed list in the
// reason, and flags the rejection as a vocabulary failure rather than a
// grammatical one.
func TestSetVocabularyRejectsAnUnlistedType(t *testing.T) {
	for _, name := range []string{Conventional, Gitmoji} {
		t.Run(name, func(t *testing.T) {
			message := "ai: discourage inflated language"
			if name == Gitmoji {
				message = ":sparkles: " + message
			}

			result := validatorFor(t, name, vocabulary).Validate(message)
			if result.Valid {
				t.Fatalf("Validate(%q) accepted an unlisted type", message)
			}
			if !result.TypeNotAllowed {
				t.Errorf("TypeNotAllowed = false; the command layer cannot tell this from freeform intent")
			}
			for _, want := range vocabulary {
				if !strings.Contains(result.Reason, want) {
					t.Errorf("reason = %q, want it to name %q", result.Reason, want)
				}
			}
		})
	}
}

// A listed type still passes, so the vocabulary is not rejecting everything.
func TestSetVocabularyAcceptsAListedType(t *testing.T) {
	for _, tc := range []struct{ name, message string }{
		{Conventional, "feat(api): add a thing"},
		{Conventional, "fix!: undo a thing"},
		{Gitmoji, ":sparkles: feat(api): add a thing"},
	} {
		if result := validatorFor(t, tc.name, vocabulary).Validate(tc.message); !result.Valid {
			t.Errorf("%s Validate(%q) rejected with %q", tc.name, tc.message, result.Reason)
		}
	}
}

// A malformed type must be reported as malformed, not as "not in the list" —
// otherwise a typo sends someone to edit their config.
func TestMalformedTypeIsNotReportedAsAVocabularyFailure(t *testing.T) {
	result := validatorFor(t, Conventional, vocabulary).Validate("Feat: add a thing")
	if result.Valid {
		t.Fatal("accepted a capitalised type")
	}
	if result.TypeNotAllowed {
		t.Errorf("reported as a vocabulary failure: %q", result.Reason)
	}
}

// A bare :shortcode: carries no commit type, so the vocabulary does not reach
// it. Constraining it would need a separate list of shortcodes.
func TestBareShortcodeIsNotHeldToTheVocabulary(t *testing.T) {
	if result := validatorFor(t, Gitmoji, vocabulary).Validate(":wip: half done"); !result.Valid {
		t.Errorf("rejected a bare shortcode with %q", result.Reason)
	}
}

// The generator has to be told the vocabulary, or it only ever learns it by
// being rejected and re-prompted.
func TestRulesStateTheVocabulary(t *testing.T) {
	for _, name := range []string{Conventional, Gitmoji} {
		t.Run(name, func(t *testing.T) {
			if rules := validatorFor(t, name, nil).Rules(); strings.Contains(rules, "must be exactly one of") {
				t.Errorf("an unset vocabulary still stated a list:\n%s", rules)
			}

			rules := validatorFor(t, name, vocabulary).Rules()
			for _, want := range vocabulary {
				if !strings.Contains(rules, want) {
					t.Errorf("rules do not name %q:\n%s", want, rules)
				}
			}
			// The shared rules must survive the insertion: the vocabulary line
			// goes before them, and appending in the wrong order would drop
			// the imperative-mood rule off the end of the prompt.
			if !strings.Contains(rules, "imperative mood") {
				t.Errorf("rules lost the shared block:\n%s", rules)
			}
		})
	}
}

// freeform-with-rules has no notion of a type, so a configured vocabulary must
// not leak into its contract.
func TestFreeformIgnoresTheVocabulary(t *testing.T) {
	v := validatorFor(t, Freeform, vocabulary)

	if result := v.Validate("just some subject"); !result.Valid {
		t.Errorf("rejected a valid freeform header with %q", result.Reason)
	}
	if strings.Contains(v.Rules(), "must be exactly one of") {
		t.Errorf("freeform rules state a type vocabulary:\n%s", v.Rules())
	}
}
