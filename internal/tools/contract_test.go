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

// .
// .
// .
// .
// .
// .
// .
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

	res, err := rt.Execute(ctx, map[string]interface{}{
		"file_path": path, "offset": float64(51), "limit": float64(10)})
	if err != nil || res.Error != "" {
		t.Fatalf("paged read: %v %s", err, res.Error)
	}
	got := strings.Split(strings.TrimSpace(res.Output), "\n")
	// .
	if n := len(got); n > 0 && strings.HasPrefix(got[n-1], "[") {
		got = got[:n-1]
	}
	if len(got) != 10 || got[0] != "line 51" || got[9] != "line 60" {
		t.Fatalf("offset=51 limit=10 returned %d line(s): first=%q last=%q — the page is not the page that was asked for",
			len(got), got[0], got[len(got)-1])
	}
	if !strings.Contains(res.Output, "continue at offset 61") {
		t.Errorf("a partial page must name where to continue, got:\n%s", res.Output)
	}

	// .
	res, _ = rt.Execute(ctx, map[string]interface{}{"file_path": path})
	if res.Output != b.String() {
		t.Error("an unpaged read must still return the whole file verbatim")
	}

	// .
	res, _ = rt.Execute(ctx, map[string]interface{}{"file_path": path, "offset": float64(500)})
	if res.Error == "" || !strings.Contains(res.Error, "past the end") {
		t.Errorf("an offset past EOF must say so, got output=%q err=%q", res.Output, res.Error)
	}

	// .
	res, _ = rt.Execute(ctx, map[string]interface{}{"file_path": path, "offset": 6.5})
	if res.Error == "" {
		t.Error("offset 6.5 must be refused rather than silently truncated to 6")
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestReadSchemaMatchesWhatItHonours(t *testing.T) {
	src, err := os.ReadFile("tool_read.go")
	if err != nil {
		t.Fatal(err)
	}
	// .
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
	props, _ := (&ReadTool{}).Parameters()["properties"].(map[string]interface{})
	for arg := range honoured {
		if _, ok := props[arg]; !ok {
			t.Errorf("read honours %q but does not advertise it", arg)
		}
	}
	// .
	// .
	for arg := range props {
		if !honoured[arg] {
			t.Errorf("read advertises %q and never reads it", arg)
		}
	}
}

// .
// .
// .
// .
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
	res, err := gt.Execute(context.Background(), map[string]interface{}{"pattern": "needle", "path": dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("600 matches against a 500 cap must set Truncated — a narrated cap that the typed record denies is how truncation goes uncounted")
	}
	if !strings.Contains(res.Output, "more may exist") {
		t.Errorf("the cap must say more may exist, got:\n%s", res.Output)
	}
}
