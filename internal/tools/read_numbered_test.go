package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readHome(t *testing.T, files map[string]string) (*Registry, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewRegistry(home, nil, Timeouts{}), home
}

func modelCall(t *testing.T, r *Registry, tool string, args map[string]any) Result {
	t.Helper()
	res, err := r.ExecuteModel(context.Background(), tool, args)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestReadNumbersEachLineWithTheFilesOwnNumber(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "l%d\n", i)
	}
	r, _ := readHome(t, map[string]string{"f.txt": b.String(), "empty.txt": ""})

	res := modelCall(t, r, "read", map[string]any{"file_path": "f.txt"})
	var want []string
	for i := 1; i <= 12; i++ {
		want = append(want, fmt.Sprintf("%6d\tl%d", i, i))
	}
	if res.Output != strings.Join(want, "\n") || res.Truncated {
		t.Errorf("a whole read is not cat -n:\n%q", res.Output)
	}
	res = modelCall(t, r, "read", map[string]any{"file_path": "f.txt", "offset": 10, "limit": 2})
	if res.Output != "    10\tl10\n    11\tl11\n[more lines — continue at offset 12]" {
		t.Errorf("a page is numbered with the file's line numbers: %q", res.Output)
	}
	if res := modelCall(t, r, "read", map[string]any{"file_path": "empty.txt"}); res.Output != "[empty file]" || res.Error != "" {
		t.Errorf("an empty file: %+v", res)
	}
	if d := (&ReadTool{}).Description(); !strings.Contains(d, "line number and a tab") || !strings.Contains(d, "not in the file") ||
		!strings.Contains(d, "old_string") {
		t.Errorf("the description must say the prefix is not part of the file: %q", d)
	}
}

// The byte cap counts the numbers; a page ends on a whole line and names
// the next one; a line longer than the cap keeps its number in every
// chunk.
func TestReadNumbersSurviveTheCapAndALongLine(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 6000; i++ {
		fmt.Fprintf(&b, "%09d\n", i) // 9 bytes of text, 7 of number
	}
	long := strings.Repeat("z", 60000)
	r, _ := readHome(t, map[string]string{"many.txt": b.String(), "long.txt": "a\nb\n" + long + "\nd\n"})

	res := modelCall(t, r, "read", map[string]any{"file_path": "many.txt"})
	i := strings.LastIndex(res.Output, "\n[")
	if i < 0 || !res.Truncated {
		t.Fatalf("a page past the cap did not say so: truncated=%v", res.Truncated)
	}
	page, note := res.Output[:i], res.Output[i+1:]
	lines := strings.Split(page, "\n")
	n := len(lines)
	if len(page) > 51200 {
		t.Errorf("the page is %d bytes: the cap must count the line numbers", len(page))
	}
	if last := fmt.Sprintf("%6d\t%09d", n, n); lines[n-1] != last {
		t.Errorf("the page's last line is %q, want %q", lines[n-1], last)
	}
	if want := fmt.Sprintf("[truncated at the byte cap — continue at offset %d]", n+1); note != want {
		t.Errorf("the continuation is %q, want %q", note, want)
	}

	res = modelCall(t, r, "read", map[string]any{"file_path": "long.txt", "offset": 3})
	if !strings.HasPrefix(res.Output, "     3\tzzz") || !strings.Contains(res.Output, "Continue with offset 3 and byte_offset 51200.") {
		t.Fatalf("the long line's first chunk: %q", res.Output[:40])
	}
	res = modelCall(t, r, "read", map[string]any{"file_path": "long.txt", "offset": 3, "byte_offset": 51200})
	if want := "     3\t" + strings.Repeat("z", 60000-51200) + "\n[end of line 3 — continue at offset 4]"; res.Output != want {
		t.Errorf("the continued line lost its number: %q…", res.Output[:min(len(res.Output), 40)])
	}
}

// An old_string copied out of a read with its numbers is refused with the
// reason, and the file is left alone. Text that merely looks numbered and
// is in the file is edited as ever.
func TestEditSaysWhenOldStringCarriesReadsNumbers(t *testing.T) {
	r, home := readHome(t, map[string]string{
		"code.go":   "package x\n\nfunc A() {}\n",
		"table.tsv": "1\tone\n2\ttwo\n",
	})
	read := modelCall(t, r, "read", map[string]any{"file_path": "code.go"})
	copied := strings.Join(strings.Split(read.Output, "\n")[2:3], "\n") // "     3\tfunc A() {}"
	res := modelCall(t, r, "edit", map[string]any{"file_path": "code.go", "old_string": copied, "new_string": "func B() {}"})
	if !strings.Contains(res.Error, "begins with a line number and a tab") {
		t.Errorf("a numbered old_string was refused without its reason: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "code.go")); string(got) != "package x\n\nfunc A() {}\n" {
		t.Errorf("the refused edit changed the file: %q", got)
	}
	res = modelCall(t, r, "edit", map[string]any{"file_path": "code.go", "old_string": "42 func C() {}", "new_string": "func B() {}"})
	if res.Error != "old_string not found in file" {
		t.Errorf("an ordinary miss: %+v", res)
	}
	res = modelCall(t, r, "edit", map[string]any{"file_path": "table.tsv", "old_string": "2\ttwo", "new_string": "2\tdeux"})
	if res.Error != "" {
		t.Errorf("text that is in the file was refused for its shape: %+v", res)
	}
}
