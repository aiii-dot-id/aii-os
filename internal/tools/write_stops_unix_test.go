//go:build !windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A WRITE THAT STOPS AFTER THE FILE WAS EMPTIED SAYS SO. Write and edit
// replace a file in place. A failure after the truncate (a full disk, a
// quota) left the file empty or part-written, and the result was the bare
// OS error: the identity read "no space left on device", believed the
// edit had not happened, and their document was gone.
//
// The failure is a real one: the test runs itself again as a child whose
// file-size limit is below what it writes, so the write stops partway
// after the truncate, as it does on a full disk.
const writeStopsEnv = "AII_TEST_WRITE_STOPS_IN"

func TestAWriteThatStopsAfterTheFileWasEmptiedSaysSo(t *testing.T) {
	if dir := os.Getenv(writeStopsEnv); dir != "" {
		writeStopsChild(dir)
		return
	}
	dir := t.TempDir()
	// Made before the limit is in force: one file that fits back under it
	// and one that does not.
	small, big := strings.Repeat("s", 1000), strings.Repeat("b", 8000)
	for name, content := range map[string]string{"edit-fits.txt": "anchor " + small, "edit-big.txt": "anchor " + big, "write.txt": small} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAWriteThatStopsAfterTheFileWasEmptiedSaysSo$")
	cmd.Env = append(os.Environ(), writeStopsEnv+"="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child that writes under a file-size limit failed: %v\n%s", err, out)
	}
	said := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name+".said"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	holds := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Edit, where what it read fits back: the file is whole again and the
	// result says the edit was not made.
	if got := holds("edit-fits.txt"); got != "anchor "+small {
		t.Errorf("AN EDIT THAT FAILED LEFT THE FILE CHANGED: it holds %d bytes, want the %d it held before", len(got), len("anchor "+small))
	}
	if got := said("edit-fits.txt"); !strings.Contains(got, "the edit was not made") || !strings.Contains(got, "has been put back") {
		t.Errorf("the result does not say the edit was not made and the content put back: %q", got)
	}
	// Edit, where even what it read no longer fits: the put-back empties
	// the file again and stops, so the file holds the head of what it held
	// BEFORE and none of the edit, and the result must say exactly that.
	before := "anchor " + big
	if got := holds("edit-big.txt"); got != before[:4096] {
		t.Errorf("rig: after a put-back that stopped, the file holds %d bytes and is not the first 4096 it held before", len(got))
	}
	got := said("edit-big.txt")
	if !strings.Contains(got, "now holds the first 4096 of the 8007 bytes it held before, and nothing of the edit") {
		t.Errorf("THE RESULT DOES NOT SAY WHAT THE FILE HOLDS after a put-back that stopped: %q", got)
	}
	if strings.Contains(got, "of this write") || strings.Contains(got, "nothing of what it held before") {
		t.Errorf("the result still describes the first write, whose bytes the put-back has replaced: %q", got)
	}
	// Write: the result says how much of the write the file holds.
	if got := said("write.txt"); !strings.Contains(got, "is left holding 4096 of the 9000 bytes") || !strings.Contains(got, "nothing of what it held before") {
		t.Errorf("A WRITE THAT STOPPED PARTWAY RETURNED THE BARE ERROR: %q", got)
	}
}

// writeStopsChild runs the three calls under a 4096-byte file-size limit
// and leaves each result beside its file.
func writeStopsChild(dir string) {
	signal.Ignore(syscall.SIGXFSZ) // the write then fails with EFBIG instead of ending the process
	limit := syscall.Rlimit{Cur: 4096, Max: 4096}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		panic(err)
	}
	grown := strings.Repeat("n", 9000)
	record := func(name string, res Result) {
		// Results are short, well under the limit.
		if err := os.WriteFile(filepath.Join(dir, name+".said"), []byte(res.Error+res.Output), 0o600); err != nil {
			panic(err)
		}
	}
	for _, name := range []string{"edit-fits.txt", "edit-big.txt"} {
		res, _ := (&EditTool{}).Execute(context.Background(), map[string]any{
			"file_path": filepath.Join(dir, name), "old_string": "anchor", "new_string": grown})
		record(name, res)
	}
	res, _ := (&WriteTool{}).Execute(context.Background(), map[string]any{
		"file_path": filepath.Join(dir, "write.txt"), "content": grown})
	record("write.txt", res)
}

// Each sentence of a failed edit says what the file holds now. The third,
// a put-back refused before it touched the file, cannot be reached without
// replacing the file between two writes, so the three are held here.
func TestAFailedEditSaysWhatTheFileHoldsNow(t *testing.T) {
	first := &emptiedError{err: syscall.ENOSPC, wrote: 10, of: 90}
	if got := editFailure("/h/doc", first, nil); !strings.Contains(got, "the edit was not made") || !strings.Contains(got, "has been put back") || strings.Contains(got, "10 of the 90") {
		t.Errorf("put back whole: %q", got)
	}
	again := &emptiedError{err: syscall.ENOSPC, wrote: 7, of: 50}
	if got := editFailure("/h/doc", first, again); !strings.Contains(got, "now holds the first 7 of the 50 bytes it held before, and nothing of the edit") || strings.Contains(got, "10 of the 90") {
		t.Errorf("a put-back that stopped: %q", got)
	}
	refused := errNotTheFileRead("/h/doc")
	if got := editFailure("/h/doc", first, refused); !strings.Contains(got, "is left holding 10 of the 90 bytes of this write") || !strings.Contains(got, "was refused") || !strings.Contains(got, "The edit was not made") {
		t.Errorf("a put-back refused before it touched the file: %q", got)
	}
}
