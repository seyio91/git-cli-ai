package conventional

import "testing"

func TestValidateAcceptsConventionalMessages(t *testing.T) {
	tests := []string{
		"feat(cli): add commit command",
		"fix: handle empty stage",
		"chore(git-core)!: change status parser",
		"build(go/mod): add cobra",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			if result := Validate(tt, nil); !result.Valid {
				t.Fatalf("expected valid message, got %q", result.Reason)
			}
		})
	}
}

func TestSC01_ValidateAcceptsOpaqueBodyAfterBlankLine(t *testing.T) {
	tests := map[string]string{
		"simple body":           "feat(api): drop v1\n\nRewrote the routing layer.",
		"breaking footer":       "feat(api)!: drop v1\n\nBREAKING CHANGE: v1 removed.\nRefs: TASK-412",
		"trailing newline":      "feat(api): drop v1\n",
		"trailing blank":        "feat(api): drop v1\n\n",
		"crlf separator":        "feat(api): drop v1\r\n\r\nRewrote the routing layer.",
		"body is not a header":  "feat(api): drop v1\n\nNot(a): conventional header at all!!\n\tindented junk",
		"body with blank lines": "feat(api): drop v1\n\npara one\n\npara two",
	}

	for name, message := range tests {
		t.Run(name, func(t *testing.T) {
			if result := Validate(message, nil); !result.Valid {
				t.Fatalf("expected valid message, got %q", result.Reason)
			}
		})
	}
}

func TestSC01_ValidateRejectsBodyWithoutBlankLine(t *testing.T) {
	for _, message := range []string{
		"feat(api): drop v1\nbody",
		"feat(api): drop v1\r\nbody",
	} {
		t.Run(message, func(t *testing.T) {
			result := Validate(message, nil)
			if result.Valid {
				t.Fatal("expected invalid message")
			}
			if result.Reason != "body must be separated from the header by a blank line" {
				t.Fatalf("unexpected reason: %q", result.Reason)
			}
		})
	}
}

func TestSC01_ValidateStillChecksHeaderWhenBodyPresent(t *testing.T) {
	if result := Validate("not a header\n\nbody", nil); result.Valid {
		t.Fatal("expected invalid header to be rejected even with a well-formed body")
	}
}

func TestValidateRejectsNonConformingMessages(t *testing.T) {
	tests := []string{
		"",
		"add commit command",
		"Feat(cli): add command",
		"feat(cli):",
		"feat(cli):  add command",
		"feat(): add command",
		"feat(cli tools): add command",
		"feat(cli: add command",
		"feat(cli): add command\nbody",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			if result := Validate(tt, nil); result.Valid {
				t.Fatal("expected invalid message")
			}
		})
	}
}
