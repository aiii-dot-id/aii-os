package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// grepHome builds a home of files and a registry over it.
func grepHome(t *testing.T, files map[string]string) (*Registry, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewRegistry(home, nil, Timeouts{}), home
}

func grepCall(t *testing.T, r *Registry, args map[string]any) Result {
	t.Helper()
	res, err := r.ExecuteModel(context.Background(), "grep", args)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestGrepIgnoreCaseAndLiteral(t *testing.T) {
	r, home := grepHome(t, map[string]string{"a.txt": "Needle in a.b(x)\naxb(x) no\n"})
	a := filepath.Join(home, "a.txt")

	if res := grepCall(t, r, map[string]any{"pattern": "needle"}); strings.Contains(res.Output, a+":1:") {
		t.Fatalf("control: a case-sensitive search matched Needle: %q", res.Output)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "needle", "ignore_case": true}); res.Output != a+":1:Needle in a.b(x)\n" {
		t.Errorf("ignore_case did not match Needle: %+v", res)
	}
	// A regex: "a.b(" does not compile, and "a.b" matches axb as well.
	if res := grepCall(t, r, map[string]any{"pattern": "a.b("}); !strings.Contains(res.Error, "invalid pattern") {
		t.Fatalf("control: a.b( compiled as a regex: %+v", res)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "a.b(", "literal": true}); res.Output != a+":1:Needle in a.b(x)\n" {
		t.Errorf("literal=true did not search the plain text: %+v", res)
	}
	// Both together, and a BRE-looking literal is not told it is BRE.
	if res := grepCall(t, r, map[string]any{"pattern": "NEEDLE IN A.B", "literal": true, "ignore_case": true}); res.Output != a+":1:Needle in a.b(x)\n" {
		t.Errorf("literal with ignore_case: %+v", res)
	}
	if res := grepCall(t, r, map[string]any{"pattern": `a\|b`, "literal": true}); strings.Contains(res.Output, "RE2") {
		t.Errorf("a literal search was given the regex dialect hint: %q", res.Output)
	}
	if res := grepCall(t, r, map[string]any{"pattern": `a\|b`}); !strings.Contains(res.Output, "RE2") {
		t.Errorf("the dialect hint is gone from a regex that found nothing: %q", res.Output)
	}
}

func TestGrepIncludeMatchesNamesOrPathsUnderTheRoot(t *testing.T) {
	r, home := grepHome(t, map[string]string{
		"top.go":                        "needle\n",
		"top.md":                        "needle\n",
		"internal/a_test.go":            "needle\n",
		"internal/tools/b_test.go":      "needle\n",
		"internal/tools/b.go":           "needle\n",
		"cmd/c_test.go":                 "needle\n",
		"internal/deep/er/more_test.go": "needle\n",
	})
	files := func(res Result) []string {
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(res.Output), "\n") {
			rel, _ := filepath.Rel(home, l)
			out = append(out, filepath.ToSlash(rel))
		}
		return out
	}
	res := grepCall(t, r, map[string]any{"pattern": "needle", "include": "*.go", "output": "files"})
	if got := strings.Join(files(res), " "); got != "cmd/c_test.go internal/a_test.go internal/deep/er/more_test.go internal/tools/b.go internal/tools/b_test.go top.go" {
		t.Errorf("include=*.go (a name glob, at any depth): %s", got)
	}
	res = grepCall(t, r, map[string]any{"pattern": "needle", "include": "internal/**/*_test.go", "output": "files"})
	if got := strings.Join(files(res), " "); got != "internal/a_test.go internal/deep/er/more_test.go internal/tools/b_test.go" {
		t.Errorf("include=internal/**/*_test.go (a path glob under the root): %s", got)
	}
	if runtime.GOOS == "windows" {
		res = grepCall(t, r, map[string]any{"pattern": "needle", "include": `internal\**\*_test.go`, "output": "files"})
		if got := strings.Join(files(res), " "); got != "internal/a_test.go internal/deep/er/more_test.go internal/tools/b_test.go" {
			t.Errorf(`include written with \ on Windows: %s`, got)
		}
	}
	// A glob that let nothing through says so: that, not the pattern, is
	// the likelier reason for the negative.
	res = grepCall(t, r, map[string]any{"pattern": "needle", "include": "*.rs"})
	if !strings.Contains(res.Output, "in 0 files matching include=*.rs") {
		t.Errorf("a negative under an include did not say what the glob admitted: %q", res.Output)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "needle", "include": "[a-"}); !strings.Contains(res.Error, "not a valid glob") {
		t.Errorf("a malformed glob searched instead of being refused: %+v", res)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "needle", "include": []any{"*.go"}}); !strings.Contains(res.Error, "include must be a string") {
		t.Errorf("a list of globs was read as no glob: %+v", res)
	}
}

func TestGrepContextIsGrepsForm(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		text := fmt.Sprintf("line %d", i)
		if i == 5 || i == 7 || i == 15 {
			text += " hit"
		}
		b.WriteString(text + "\n")
	}
	r, home := grepHome(t, map[string]string{"f.txt": b.String(), "g.txt": "hit\nafter\n"})
	f, g := filepath.Join(home, "f.txt"), filepath.Join(home, "g.txt")
	res := grepCall(t, r, map[string]any{"pattern": "hit", "context": 1})
	want := f + "-4-line 4\n" +
		f + ":5:line 5 hit\n" +
		f + "-6-line 6\n" +
		f + ":7:line 7 hit\n" +
		f + "-8-line 8\n" +
		"--\n" +
		f + "-14-line 14\n" +
		f + ":15:line 15 hit\n" +
		f + "-16-line 16\n" +
		"--\n" +
		g + ":1:hit\n" +
		g + "-2-after\n"
	if res.Output != want || res.Truncated {
		t.Errorf("context=1:\n got %q\nwant %q", res.Output, want)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "hit", "context": 101}); !strings.Contains(res.Error, "context must be between 0 and 100") {
		t.Errorf("an unbounded context was accepted: %+v", res)
	}
}

func TestGrepOutputFilesAndCount(t *testing.T) {
	r, home := grepHome(t, map[string]string{"a.txt": "x\nx\nx\n", "b/c.txt": "x\n", "d.txt": "y\n"})
	a, c := filepath.Join(home, "a.txt"), filepath.Join(home, "b", "c.txt")
	if res := grepCall(t, r, map[string]any{"pattern": "x", "output": "files"}); res.Output != a+"\n"+c+"\n" {
		t.Errorf("output=files: %q", res.Output)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "x", "output": "count"}); res.Output != a+":3\n"+c+":1\n" {
		t.Errorf("output=count: %q", res.Output)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "x", "output": "lines"}); !strings.Contains(res.Error, "output must be content, files or count") {
		t.Errorf("an unknown output was accepted: %+v", res)
	}
}

// Pages: exact totals, the next offset, a stable order, and a typed
// Truncated on every page that leaves entries behind it.
func TestGrepPagesWithExactTotals(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 41; i++ {
		var b strings.Builder
		n := 8 // 41 files, 347 matches: the shape of the example in the plan
		if i < 19 {
			n = 9
		}
		for j := 0; j < n; j++ {
			fmt.Fprintf(&b, "needle %d.%d\n", i, j)
		}
		files[fmt.Sprintf("d%02d/f.txt", i)] = b.String()
	}
	r, home := grepHome(t, files)
	// EVERY PAGE MUST FIT THE BYTE CAP, whatever the temporary directory's
	// length: a match line carries the home's absolute path, so on a host
	// whose temporary paths are long, 347 lines (or 100) can pass pageBytes
	// and the cap — not the entry count this test is about — ends the page.
	// The page sizes are computed from the longest match line, so the test
	// asks the same questions on a short path and a long one.
	longest := len(filepath.Join(home, "d40", "f.txt")) + len(":9:needle 40.8\n")
	step := min(100, (pageBytes-512)/longest) // 512: room for the page note
	if step < 2 {
		t.Fatalf("inconclusive: a match line is %d bytes, too long for two to a page", longest)
	}
	// The whole search, read in pages of another size, so the boundaries
	// differ from the ones checked below.
	alt := max(1, step*2/5)
	var whole strings.Builder
	total := 0
	for offset := 0; ; offset += alt {
		res := grepCall(t, r, map[string]any{"pattern": "needle", "offset": offset, "limit": alt})
		lines := strings.SplitAfter(res.Output, "\n")
		n := min(alt, 347-offset)
		if n <= 0 || len(lines) < n+1 {
			t.Fatalf("inconclusive: offset=%d gave %d lines: %q", offset, len(lines), res.Output)
		}
		whole.WriteString(strings.Join(lines[:n], ""))
		total += n
		if !res.Truncated {
			break
		}
	}
	if total != 347 || strings.Count(whole.String(), "\n") != 347 {
		t.Fatalf("inconclusive: the whole search is %d lines, want 347", total)
	}
	var pages strings.Builder
	for offset := 0; offset < total; offset += step {
		args := map[string]any{"pattern": "needle", "offset": offset}
		if step < 100 {
			args["limit"] = step // the default page (100) does not fit this path
		}
		res := grepCall(t, r, args)
		lines := strings.SplitAfter(res.Output, "\n")
		end := min(offset+step, total)
		note := fmt.Sprintf("[showing %d–%d of 347 matches in 41 files", offset+1, end)
		if end < total {
			note += fmt.Sprintf("; continue with offset=%d", end)
		}
		if got := lines[end-offset]; got != note+"]\n" {
			t.Errorf("offset=%d: the page note is %q, want %q", offset, got, note+"]\n")
		}
		if res.Truncated != (end < total) {
			t.Errorf("offset=%d: Truncated=%v, but entries after this page: %v", offset, res.Truncated, end < total)
		}
		pages.WriteString(strings.Join(lines[:end-offset], ""))
	}
	if pages.String() != whole.String() {
		t.Error("the pages joined are not the whole search: the order is not stable")
	}
	res := grepCall(t, r, map[string]any{"pattern": "needle", "offset": 400})
	if !strings.Contains(res.Output, "[offset=400 is past the end: 347 matches in 41 files]") {
		t.Errorf("an offset past the end: %q", res.Output)
	}
	res = grepCall(t, r, map[string]any{"pattern": "needle", "output": "files", "limit": 10, "offset": 10})
	if !strings.Contains(res.Output, "[showing 11–20 of 41 files with matches; continue with offset=20]") {
		t.Errorf("a page of files: %q", res.Output)
	}
	res = grepCall(t, r, map[string]any{"pattern": "needle", "output": "count", "limit": 40})
	if !strings.Contains(res.Output, "[showing 1–40 of 41 files with matches (347 matches); continue with offset=40]") {
		t.Errorf("a page of counts: %q", res.Output)
	}
	if res := grepCall(t, r, map[string]any{"pattern": "needle", "limit": -1}); !strings.Contains(res.Error, "limit must not be negative") {
		t.Errorf("a negative limit: %+v", res)
	}
}

// The ceilings stay: 500 entries and 50 KB a page. A page cut by the
// byte cap says so and continues at the first entry it did not show; one
// enormous line is shown in part, around its match, and the page still
// advances.
func TestGrepKeepsItsCeilings(t *testing.T) {
	var many strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&many, "needle %d\n", i)
	}
	long := strings.Repeat("x", 200) + " needle " + strings.Repeat("y", 200)
	var wide strings.Builder
	for i := 0; i < 200; i++ {
		wide.WriteString(long + "\n")
	}
	huge := strings.Repeat("a", 20000) + "THE-NEEDLE" + strings.Repeat("b", 20000) + "\n"
	r, home := grepHome(t, map[string]string{"many.txt": many.String(), "wide.txt": wide.String(), "huge.txt": huge})

	// Held to 500 entries; on a host whose temporary paths are long, the
	// byte cap ends the page first, so the clamp itself is pinned on
	// pageArgs as well.
	res := grepCall(t, r, map[string]any{"pattern": "needle", "path": "many.txt", "limit": 1000})
	n := strings.Count(res.Output, "\n") - 1
	if n > 500 || !strings.Contains(res.Output, fmt.Sprintf("[showing 1–%d of 600 matches in 1 file", n)) ||
		!strings.Contains(res.Output, fmt.Sprintf("continue with offset=%d]", n)) {
		t.Errorf("limit=1000 must be held to 500: %d lines, tail %q", n, res.Output[len(res.Output)-120:])
	}
	for asked, held := range map[any]int{nil: 100, 0: 100, 7: 7, 500: 500, 1000: 500} {
		if _, limit, err := pageArgs(map[string]any{"limit": asked}); err != nil || limit != held {
			t.Errorf("limit %v is held to %d (%v), want %d", asked, limit, err, held)
		}
	}

	res = grepCall(t, r, map[string]any{"pattern": "needle", "path": "wide.txt", "limit": 500})
	if len(res.Output) > pageBytes+200 || !res.Truncated {
		t.Fatalf("the byte cap did not hold: %d bytes, truncated=%v", len(res.Output), res.Truncated)
	}
	shown := strings.Count(res.Output, ":"+long+"\n")
	note := fmt.Sprintf("[showing 1–%d of 200 matches in 1 file (the page reached the 50 KB output cap); continue with offset=%d]", shown, shown)
	if shown == 0 || !strings.Contains(res.Output, note) {
		t.Errorf("a page cut by the byte cap must say where to continue (want %q): tail %q", note, res.Output[len(res.Output)-160:])
	}

	res = grepCall(t, r, map[string]any{"pattern": "THE-NEEDLE", "path": "huge.txt"})
	h := filepath.Join(home, "huge.txt")
	if !strings.HasPrefix(res.Output, h+":1:…") || !strings.Contains(res.Output, "THE-NEEDLE") ||
		!strings.Contains(res.Output, "[clipped: line 1 is 40010 bytes; read offset=1 shows it]") || len(res.Output) > 1000 || !res.Truncated {
		t.Errorf("a 40 KB line was not shown in part around its match: truncated=%v %q", res.Truncated, res.Output)
	}
}

// .git and node_modules are not entered on the way down; named as the
// path, they are what was asked for.
func TestGrepSearchesATreeItIsAskedToStartIn(t *testing.T) {
	r, home := grepHome(t, map[string]string{".git/config": "needle\n", "node_modules/m.js": "needle\n", "a.txt": "needle\n"})
	if res := grepCall(t, r, map[string]any{"pattern": "needle"}); res.Output != filepath.Join(home, "a.txt")+":1:needle\n"+
		"\n[coverage: 2 directory tree(s) skipped by policy (.git, node_modules)]" {
		t.Errorf("control: %q", res.Output)
	}
	for _, tree := range []string{".git", "node_modules"} {
		if res := grepCall(t, r, map[string]any{"pattern": "needle", "path": tree}); !strings.HasPrefix(res.Output, filepath.Join(home, tree)) {
			t.Errorf("path=%s was not searched: %q", tree, res.Output)
		}
	}
}
