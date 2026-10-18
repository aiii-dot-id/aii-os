package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReadPagesWhereItSaysItPages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hundred.txt")
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &ReadTool{maxBytes: 51200}
	ctx := context.Background()

	res, err := rt.Execute(ctx, map[string]any{
		"file_path": path, "offset": float64(51), "limit": float64(10)})
	if err != nil || res.Error != "" {
		t.Fatalf("paged read: %v %s", err, res.Error)
	}
	got := strings.Split(res.Output, "\n")
	// The continuation notice is the last line when more remains.
	if n := len(got); n > 0 && strings.HasPrefix(got[n-1], "[") {
		got = got[:n-1]
	}
	// Numbered with the file's own line numbers, not the page's.
	if len(got) != 10 || got[0] != "    51\tline 51" || got[9] != "    60\tline 60" {
		t.Fatalf("offset=51 limit=10 returned %d line(s): first=%q last=%q — the page is not the page that was asked for",
			len(got), got[0], got[len(got)-1])
	}
	if !strings.Contains(res.Output, "continue at offset 61") {
		t.Errorf("a partial page must name where to continue, got:\n%s", res.Output)
	}

	// The default call returns the whole file, every line numbered.
	res, _ = rt.Execute(ctx, map[string]any{"file_path": path})
	var numbered []string
	for i := 1; i <= 100; i++ {
		numbered = append(numbered, fmt.Sprintf("%6d\tline %d", i, i))
	}
	if res.Output != strings.Join(numbered, "\n") {
		t.Errorf("an unpaged read must return the whole file, numbered: %q", res.Output[:min(len(res.Output), 80)])
	}

	// Past the end is said, not silently answered with the head.
	res, _ = rt.Execute(ctx, map[string]any{"file_path": path, "offset": float64(500)})
	if res.Error == "" || !strings.Contains(res.Error, "past the end") {
		t.Errorf("an offset past EOF must say so, got output=%q err=%q", res.Output, res.Error)
	}

	// A non-integral line number is a mistake, not something to round.
	res, _ = rt.Execute(ctx, map[string]any{"file_path": path, "offset": 6.5})
	if res.Error == "" {
		t.Error("offset 6.5 must be refused rather than silently truncated to 6")
	}
}

// Every argument Execute honours must be advertised, or a strict
// provider refuses what the tool actually supports and a permissive one
// passes it through to be ignored.
//
// The argument list is READ OUT OF THE SOURCE, not typed here. It used
// to be a hand-kept slice — a copy of the contract, maintained beside
// the contract — and when byte_offset was added to Execute the copy did
// not know. I caught that by eye, one commit before shipping it, which
// is not a control. A guard that must be updated by the same edit it
// guards against is not guarding anything.
func TestReadSchemaMatchesWhatItHonours(t *testing.T) {
	src, err := os.ReadFile("tool_read.go")
	if err != nil {
		t.Fatal(err)
	}
	// Both shapes Execute uses to reach an argument.
	pat := regexp.MustCompile(`args\["([a-z_]+)"\]|intArg\(args, "([a-z_]+)"`)
	honoured := map[string]bool{}
	for _, m := range pat.FindAllStringSubmatch(string(src), -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		honoured[name] = true
	}
	if len(honoured) < 3 {
		t.Fatalf("found only %d argument(s) in tool_read.go — the scan is broken, not the tool", len(honoured))
	}
	props, _ := (&ReadTool{}).Parameters()["properties"].(map[string]any)
	for arg := range honoured {
		if _, ok := props[arg]; !ok {
			t.Errorf("read honours %q but does not advertise it", arg)
		}
	}
	// And nothing advertised that Execute never reads — the other
	// direction of the same lie.
	for arg := range props {
		if !honoured[arg] {
			t.Errorf("read advertises %q and never reads it", arg)
		}
	}
}

// TRUNCATION MUST BE TYPED, not merely narrated. grep appended a
// sentence about capping while leaving Result.Truncated false, so
// the record's truncated flag held none of it and the loop's typed
// observation disagreed with the prose the model could read.
func TestGrepTruncationIsTyped(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "needle %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	gt := &GrepTool{}
	res, err := gt.Execute(context.Background(), map[string]any{"pattern": "needle", "path": dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("600 matches on a 100-entry page must set Truncated — a narrated cap that the typed record denies is how truncation goes uncounted")
	}
	if !strings.Contains(res.Output, "[showing 1–100 of 600 matches in 1 file; continue with offset=100]") {
		t.Errorf("the page must state the totals and where to continue, got:\n%s", res.Output)
	}
}
