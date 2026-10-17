package atomicfile

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAReplacedFileDoesNotStayReadableUnderAnotherName(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	other := filepath.Join(dir, "elsewhere.txt")
	if err := os.WriteFile(target, []byte("THE EARLIER SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, other); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	published, err := WriteReplace(target, []byte("the new contents"), 0o600)
	if err != nil || !published {
		t.Fatalf("WriteReplace over a file with another name: published=%v err=%v", published, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "the new contents" {
		t.Errorf("the name holds the new contents: %q", got)
	}
	if got, err := os.ReadFile(other); err != nil || len(got) != 0 {
		t.Errorf("the other name of the replaced file holds nothing of it: %q %v", got, err)
	}

	// Replace itself, as its other callers use it.
	if err := os.WriteFile(target, []byte("A SECOND SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	third := filepath.Join(dir, "third.txt")
	if err := os.Link(target, third); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(staged, []byte("replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if published, err := Replace(staged, target); err != nil || !published {
		t.Fatalf("Replace over a file with another name: published=%v err=%v", published, err)
	}
	if got, err := os.ReadFile(third); err != nil || len(got) != 0 {
		t.Errorf("Replace: the other name of the replaced file holds nothing of it: %q %v", got, err)
	}

	// The control half: one name, a reader that opened it before.
	single := filepath.Join(dir, "single.json")
	if err := os.WriteFile(single, []byte("the version a reader holds"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(single)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if published, err := WriteReplace(single, []byte("next"), 0o600); err != nil || !published {
		t.Fatalf("WriteReplace over a file with one name: published=%v err=%v", published, err)
	}
	if got, err := io.ReadAll(reader); err != nil || string(got) != "the version a reader holds" {
		t.Errorf("a reader that opened the file with one name before the save reads its version whole: %q %v", got, err)
	}
	if got, _ := os.ReadFile(single); string(got) != "next" {
		t.Errorf("the file with one name is replaced: %q", got)
	}
}

func TestAReadOnlyFileWithAnotherNameIsReplacedAndEmptied(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "providers.json")
	other := filepath.Join(dir, "elsewhere.txt")
	if err := os.WriteFile(target, []byte("THE EARLIER SECRET"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, other); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	published, err := WriteReplace(target, []byte("the new contents"), 0o600)
	if err != nil || !published {
		t.Fatalf("a read-only file with another name is replaced: published=%v err=%v", published, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "the new contents" {
		t.Errorf("the name holds the new contents: %q", got)
	}
	if got, err := os.ReadFile(other); err != nil || len(got) != 0 {
		t.Errorf("the other name of the read-only file holds nothing of it: %q %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("nothing but the two names is left in the directory: %v", entries)
	}
	single := filepath.Join(dir, "single.json")
	if err := os.WriteFile(single, []byte("old"), 0o400); err != nil {
		t.Fatal(err)
	}
	published, err = WriteReplace(single, []byte("new"), 0o600)
	if runtime.GOOS == "windows" {
		if err == nil || published {
			t.Errorf("on Windows a file with one name and the read-only attribute is refused its replace, as before: published=%v err=%v", published, err)
		}
		if got, _ := os.ReadFile(single); string(got) != "old" {
			t.Errorf("on Windows the refused file keeps what it held: %q", got)
		}
		return
	}
	if err != nil || !published {
		t.Errorf("a file with one name at mode 0400 is replaced as before: published=%v err=%v", published, err)
	}
}
