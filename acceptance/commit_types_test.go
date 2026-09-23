package acceptance

import (
	"strings"
	"testing"
)

// Criteria for `commit.types`, the opt-in closed type vocabulary. The
// conventional validator has always checked the shape of a type and never its
// name, so a generated message could invent one — and because branch.pattern
// is {type}/{slug}, an invented type became a branch namespace.

// configList reads a list-valued config key out of the JSON payload.
func configList(t *testing.T, r result, path string) []string {
	t.Helper()

	var current any = decodeConfig(t, r).Config
	for _, segment := range strings.Split(path, ".") {
		block, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("config path %q: %q is not a table", path, segment)
		}
		current, ok = block[segment]
		if !ok {
			return nil // omitempty: an unset list is absent, not null
		}
	}

	raw, ok := current.([]any)
	if !ok {
		t.Fatalf("config path %q is not a list: %#v", path, current)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(string))
	}
	return out
}

// T1 — a vocabulary that cannot do its job is refused at load, naming the key
// and the file. An empty list is the interesting one: reading it as "any type"
// would be the widest possible reading of the narrowest possible instruction.
func TestCommitTypes_InvalidVocabularyIsRejectedAtLoad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		names string
	}{
		{"empty list", "[]", "empty"},
		{"capitalised", `["Feat"]`, "Feat"},
		{"trailing bang", `["feat!"]`, "feat!"},
		{"duplicate", `["feat", "feat"]`, "feat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			writeRepoConfig(t, repo, "[commit]\ntypes = "+tc.value+"\n")

			res := run(t, repo, "config", "--json")
			if res.exitCode == 0 {
				t.Fatalf("exit = 0, want an error (stdout %q)", res.stdout)
			}

			p := res.payload(t)
			if !strings.Contains(p.Error, "commit.types") {
				t.Errorf("error = %q, want it to name the key", p.Error)
			}
			if !strings.Contains(p.Error, ".git-cli.toml") {
				t.Errorf("error = %q, want it to name the file", p.Error)
			}
			if !strings.Contains(p.Error, tc.names) {
				t.Errorf("error = %q, want it to name %q", p.Error, tc.names)
			}
			if !strings.Contains(p.Hint, "remove the key") {
				t.Errorf("hint = %q, want it to say how to get the open vocabulary back", p.Hint)
			}
		})
	}
}

// T2 — config reports the vocabulary and which layer set it, and reports the
// unset case as the default rather than omitting the key.
func TestCommitTypes_ConfigReportsTheVocabulary(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		repo := newRepo(t)
		writeRepoConfig(t, repo, "[commit]\ntypes = [\"feat\", \"fix\", \"chore\"]\n")

		res := run(t, repo, "config", "--json")
		if res.exitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.exitCode, res.stderr)
		}

		got := configList(t, res, "commit.types")
		if strings.Join(got, ",") != "feat,fix,chore" {
			t.Errorf("commit.types = %v, want [feat fix chore]", got)
		}
		if layer := configSource(t, res, "commit.types"); layer != "repo" {
			t.Errorf("commit.types provenance = %q, want repo", layer)
		}
	})

	t.Run("unset", func(t *testing.T) {
		repo := newRepo(t)

		res := run(t, repo, "config", "--json")
		if res.exitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.exitCode, res.stderr)
		}

		if got := configList(t, res, "commit.types"); len(got) != 0 {
			t.Errorf("commit.types = %v, want empty when unset", got)
		}
		if layer := configSource(t, res, "commit.types"); layer != "default" {
			t.Errorf("commit.types provenance = %q, want default", layer)
		}

		// The human output must still carry the line: "any type is accepted"
		// is a state of the setting, not the absence of one.
		plain := run(t, repo, "config")
		if !strings.Contains(plain.stdout, "commit.types = [] (default)") {
			t.Errorf("config output does not report an unset vocabulary:\n%s", plain.stdout)
		}
	})
}
