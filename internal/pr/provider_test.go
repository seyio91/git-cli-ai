package pr

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeGH returns a GH pointed at a script that records its argv and stdin
// instead of talking to a forge. GH.Binary exists precisely so the command it
// runs can be substituted; everything below asserts on the argv that reaches it,
// not merely on the outcome, because "did it send --title" is the whole question
// for a partial update.
func fakeGH(t *testing.T) (GH, func() ([]string, string)) {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "gh-fake")
	argvFile := filepath.Join(dir, "argv")
	stdinFile := filepath.Join(dir, "stdin")

	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argvFile + "'\ncat > '" + stdinFile + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	return GH{Binary: bin}, func() ([]string, string) {
		raw, err := os.ReadFile(argvFile)
		if err != nil {
			return nil, "" // never invoked
		}
		stdin, _ := os.ReadFile(stdinFile)
		return strings.Split(strings.TrimRight(string(raw), "\n"), "\n"), string(stdin)
	}
}

func str(s string) *string { return &s }

func TestUpdatePRSendsOnlyTheFieldsThatWereSet(t *testing.T) {
	cases := []struct {
		name      string
		req       Update
		wantArgv  []string
		wantStdin string
	}{
		{
			name:      "body only",
			req:       Update{Head: "fix/thing", Body: str("## Description\nnew prose\n")},
			wantArgv:  []string{"pr", "edit", "fix/thing", "--body-file", "-"},
			wantStdin: "## Description\nnew prose\n",
		},
		{
			// The title must not appear when it was not asked for: a bare
			// --update regenerates the body and leaves the title alone.
			name:     "title only",
			req:      Update{Head: "fix/thing", Title: str("fix: reword")},
			wantArgv: []string{"pr", "edit", "fix/thing", "--title", "fix: reword"},
		},
		{
			name:      "both",
			req:       Update{Head: "fix/thing", Title: str("fix: reword"), Body: str("body")},
			wantArgv:  []string{"pr", "edit", "fix/thing", "--title", "fix: reword", "--body-file", "-"},
			wantStdin: "body",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh, captured := fakeGH(t)
			if err := gh.UpdatePR(context.Background(), tc.req); err != nil {
				t.Fatalf("UpdatePR: %v", err)
			}

			argv, stdin := captured()
			if !reflect.DeepEqual(argv, tc.wantArgv) {
				t.Errorf("argv = %q, want %q", argv, tc.wantArgv)
			}
			if stdin != tc.wantStdin {
				t.Errorf("stdin = %q, want %q", stdin, tc.wantStdin)
			}
		})
	}
}

// An Update with nothing set must not reach the forge at all. `gh pr edit` with
// no flags would edit nothing while still costing a round trip and a chance to
// fail on a repository the token cannot write.
func TestUpdatePRWithNothingSetMakesNoCall(t *testing.T) {
	gh, captured := fakeGH(t)

	if err := gh.UpdatePR(context.Background(), Update{Head: "fix/thing"}); err != nil {
		t.Fatalf("UpdatePR: %v", err)
	}

	if argv, _ := captured(); argv != nil {
		t.Errorf("invoked the forge with %q, want no call", argv)
	}
}

// A 403 has to name the operation that was refused. The hint used to say
// "cannot open a pull request" no matter what had run, which on an edit points
// at a permission that is not the one missing.
func TestGHHintNamesTheOperationThatWasRefused(t *testing.T) {
	for _, tc := range []struct{ operation, want string }{
		{"pr edit", "gh pr edit"},
		{"pr create", "gh pr create"},
	} {
		hint := ghHint(nil, "HTTP 403: Resource not accessible by integration", tc.operation)
		if !strings.Contains(hint, tc.want) {
			t.Errorf("hint for %q = %q, want it to name %q", tc.operation, hint, tc.want)
		}
	}
}

// The never-merge invariant, asserted against the interface itself rather than
// against a comment. Adding UpdatePR is the closest this interface has come to
// acting on a pull request, which is exactly when this is worth pinning down.
func TestProviderDeclaresNoMergeMethod(t *testing.T) {
	iface := reflect.TypeOf((*Provider)(nil)).Elem()

	for i := 0; i < iface.NumMethod(); i++ {
		if name := iface.Method(i).Name; strings.Contains(strings.ToLower(name), "merge") {
			t.Errorf("Provider declares %q; this tool must not be able to merge", name)
		}
	}
}
