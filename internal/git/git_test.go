package git

import (
	"reflect"
	"testing"
)

func TestParseStatusClassifiesEntries(t *testing.T) {
	status := parseStatus("M  staged.go\x00 M unstaged.go\x00AM both.go\x00 D deleted.go\x00?? new.go\x00R  renamed.go\x00old.go\x00")

	if got, want := status.StagedFiles(), []string{"both.go", "renamed.go", "staged.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("staged files = %#v, want %#v", got, want)
	}

	if got, want := status.UnstagedFiles(), []string{"both.go", "deleted.go", "new.go", "unstaged.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unstaged files = %#v, want %#v", got, want)
	}

	if got, want := status.CommitCandidateFiles(true), []string{"both.go", "deleted.go", "renamed.go", "staged.go", "unstaged.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate files = %#v, want %#v", got, want)
	}
}

func TestHasOnlyUntrackedChanges(t *testing.T) {
	if !parseStatus("?? new.go\x00?? other.go\x00").HasOnlyUntrackedChanges() {
		t.Fatal("expected only untracked changes")
	}

	if parseStatus("?? new.go\x00 M tracked.go\x00").HasOnlyUntrackedChanges() {
		t.Fatal("expected mixed changes")
	}
}

func TestSC02_ParseStatusPreservesLiteralArrowInPath(t *testing.T) {
	status := parseStatus("A  weird -> name.txt\x00")

	if got, want := status.StagedFiles(), []string{"weird -> name.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("staged files = %#v, want %#v", got, want)
	}
	if got := status.Entries[0].OrigPath; got != "" {
		t.Fatalf("non-rename entry gained an OrigPath: %q", got)
	}
}

func TestSC02_ParseStatusHandlesRenameFields(t *testing.T) {
	status := parseStatus("R  new name.txt\x00old -> name.txt\x00")

	if len(status.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %#v", len(status.Entries), status.Entries)
	}
	entry := status.Entries[0]
	if entry.Path != "new name.txt" {
		t.Fatalf("path = %q, want %q", entry.Path, "new name.txt")
	}
	if entry.OrigPath != "old -> name.txt" {
		t.Fatalf("orig path = %q, want %q", entry.OrigPath, "old -> name.txt")
	}
}

func TestSC02_ParseStatusPreservesNonASCIIPath(t *testing.T) {
	status := parseStatus(" M café thing.txt\x00")

	if got, want := status.UnstagedFiles(), []string{"café thing.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unstaged files = %#v, want %#v", got, want)
	}
}

func TestSC02_ParseStatusDoesNotConsumeFollowingEntryForNonRename(t *testing.T) {
	status := parseStatus("A  first.txt\x00A  second.txt\x00")

	if got, want := status.StagedFiles(), []string{"first.txt", "second.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("staged files = %#v, want %#v", got, want)
	}
}

func TestSC02_ParseStatusToleratesTruncatedRenameRecord(t *testing.T) {
	status := parseStatus("R  renamed.txt\x00")

	if len(status.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(status.Entries))
	}
	if status.Entries[0].OrigPath != "" {
		t.Fatalf("expected empty OrigPath on truncated record")
	}
}
