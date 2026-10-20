package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// lsTree writes files (a name ending in / is a directory) and gives each
// the modification time t0 plus its index in minutes, deepest first so a
// directory's time is not moved by what is made inside it.
func lsTree(t *testing.T, home string, names []string) time.Time {
	t.Helper()
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, n := range names {
		p := filepath.Join(home, filepath.FromSlash(n))
		if strings.HasSuffix(n, "/") {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := len(names) - 1; i >= 0; i-- {
		at := t0.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(home, filepath.FromSlash(names[i])), at, at); err != nil {
			t.Fatal(err)
		}
	}
	return t0
}

func lsCall(t *testing.T, r *Registry, args map[string]any) Result {
	t.Helper()
	return modelCall(t, r, "ls", args)
}

// names is the name column of a listing, the note lines left out.
func names(out string) []string {
	var got []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if f := strings.SplitN(l, "  ", 4); len(f) == 4 {
			got = append(got, filepath.ToSlash(f[3]))
		}
	}
	return got
}

func TestLsShowsTypeTimeSizeAndName(t *testing.T) {
	r, home := readHome(t, nil)
	t0 := lsTree(t, home, []string{"sub/", "sub/inner.txt", "a.txt"})
	stamp := func(i int) string { return t0.Add(time.Duration(i) * time.Minute).Format("2006-01-02T15:04:05Z") }
	if runtime.GOOS != "windows" {
		if err := os.Symlink("a.txt", filepath.Join(home, "ln")); err != nil {
			t.Fatal(err)
		}
	}
	res := lsCall(t, r, map[string]any{})
	want := "f  " + stamp(2) + "  5  a.txt\n"
	if runtime.GOOS != "windows" {
		want += "l  "
	}
	if !strings.HasPrefix(res.Output, want) || res.Truncated {
		t.Errorf("ls shows type, UTC time, size and name, by name:\n%s", res.Output)
	}
	if !strings.HasSuffix(res.Output, "d  "+stamp(0)+"  -  sub"+string(filepath.Separator)+"\n") {
		t.Errorf("a directory shows its time and no size:\n%s", res.Output)
	}
	if runtime.GOOS != "windows" && !strings.Contains(res.Output, "  -  ln -> a.txt\n") {
		t.Errorf("a link shows where it points:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "inner.txt") {
		t.Errorf("a listing without recursive went below its directory:\n%s", res.Output)
	}
	if res := lsCall(t, r, map[string]any{"path": "a.txt"}); res.Output != "f  "+stamp(2)+"  5  a.txt\n" {
		t.Errorf("a file lists itself: %+v", res)
	}
	if res := lsCall(t, r, map[string]any{"path": "sub", "pattern": "*.md"}); !strings.HasPrefix(res.Output, "no entries under ") ||
		!strings.Contains(res.Output, "matching pattern=*.md") {
		t.Errorf("an empty answer says what was asked: %+v", res)
	}
}

func TestLsRecursesWithAPatternAndADepth(t *testing.T) {
	depth := (&LsTool{}).Parameters()["properties"].(map[string]any)["depth"].(map[string]any)
	if words, _ := depth["description"].(string); !strings.Contains(words, "requires recursive=true") || !strings.Contains(words, "With recursive=true, zero means no depth bound") {
		t.Fatalf("the advertised depth must name its existing dependency and zero behavior: %v", depth)
	}
	r, home := readHome(t, nil)
	lsTree(t, home, []string{"a/b/c/deep.go", "a/b/notes.md", "a/x.go", "top.go", ".git/config", "node_modules/m.js"})

	res := lsCall(t, r, map[string]any{"recursive": true})
	if got := strings.Join(names(res.Output), " "); got != ".git/ a/ a/b/ a/b/c/ a/b/c/deep.go a/b/notes.md a/x.go node_modules/ top.go" {
		t.Errorf("recursive: %s", got)
	}
	if !strings.Contains(res.Output, "[coverage: 2 directory tree(s) skipped by policy (.git, node_modules)]") {
		t.Errorf("recursion must say what it did not enter:\n%s", res.Output)
	}
	res = lsCall(t, r, map[string]any{"recursive": true, "pattern": "*.go"})
	if got := strings.Join(names(res.Output), " "); got != "a/b/c/deep.go a/x.go top.go" {
		t.Errorf("pattern=*.go at any depth: %s", got)
	}
	res = lsCall(t, r, map[string]any{"recursive": true, "pattern": "a/**/*.go"})
	if got := strings.Join(names(res.Output), " "); got != "a/b/c/deep.go a/x.go" {
		t.Errorf("pattern=a/**/*.go: %s", got)
	}
	if runtime.GOOS == "windows" {
		res = lsCall(t, r, map[string]any{"recursive": true, "pattern": `a\**\*.go`})
		if got := strings.Join(names(res.Output), " "); got != "a/b/c/deep.go a/x.go" {
			t.Errorf(`a pattern written with \ on Windows: %s`, got)
		}
	}
	res = lsCall(t, r, map[string]any{"recursive": true, "depth": 2})
	if got := strings.Join(names(res.Output), " "); got != ".git/ a/ a/b/ a/x.go node_modules/ top.go" {
		t.Errorf("depth=2: %s", got)
	}
	if !strings.Contains(res.Output, "1 directory(ies) at depth 2 not entered") {
		t.Errorf("a depth bound must say what it did not enter:\n%s", res.Output)
	}
	if res := lsCall(t, r, map[string]any{"depth": 2}); !strings.Contains(res.Error, "pass recursive=true") {
		t.Errorf("depth without recursive: %+v", res)
	}
	// Asked for by name, a tree the walk does not enter is listed.
	if got := strings.Join(names(lsCall(t, r, map[string]any{"path": "node_modules", "recursive": true}).Output), " "); got != "m.js" {
		t.Errorf("node_modules named as the path: %s", got)
	}
}

func TestLsSortsByTimeAndPagesWithExactTotals(t *testing.T) {
	r, home := readHome(t, nil)
	var files []string
	for i := 0; i < 250; i++ {
		// Times in an order that is not the names', the newest walked
		// early: time order keeps only a page's worth as it walks.
		files = append(files, fmt.Sprintf("f%03d", 249-(i*7)%250))
	}
	lsTree(t, home, files)

	whole := lsCall(t, r, map[string]any{"limit": 500})
	if n := len(names(whole.Output)); n != 250 || whole.Truncated {
		t.Fatalf("inconclusive: the whole listing is %d entries (truncated=%v)", n, whole.Truncated)
	}
	var joined []string
	for offset := 0; offset < 250; offset += 100 {
		res := lsCall(t, r, map[string]any{"offset": offset})
		end := min(offset+100, 250)
		note := fmt.Sprintf("[showing %d–%d of 250 entries", offset+1, end)
		if end < 250 {
			note += fmt.Sprintf("; continue with offset=%d", end)
		}
		if !strings.Contains(res.Output, note+"]\n") || res.Truncated != (end < 250) {
			t.Errorf("offset=%d: want %q, truncated=%v:\n%s", offset, note, res.Truncated, res.Output[len(res.Output)-80:])
		}
		joined = append(joined, names(res.Output)...)
	}
	if strings.Join(joined, " ") != strings.Join(names(whole.Output), " ") {
		t.Error("the pages joined are not the whole listing")
	}

	res := lsCall(t, r, map[string]any{"sort": "mtime", "limit": 3})
	if got := strings.Join(names(res.Output), " "); got != fmt.Sprintf("%s %s %s", files[249], files[248], files[247]) {
		t.Errorf("sort=mtime is newest first: %s", got)
	}
	res = lsCall(t, r, map[string]any{"sort": "mtime", "offset": 247})
	if got := strings.Join(names(res.Output), " "); got != fmt.Sprintf("%s %s %s", files[2], files[1], files[0]) ||
		!strings.Contains(res.Output, "[showing 248–250 of 250 entries]") {
		t.Errorf("the last page by time: %s\n%s", got, res.Output)
	}
	if res := lsCall(t, r, map[string]any{"sort": "size"}); !strings.Contains(res.Error, "sort must be name or mtime") {
		t.Errorf("an unknown sort: %+v", res)
	}
}

// The walk applies grep's floor: what read refuses is not named, and is
// counted; in a granted root the home's kept names are the grant's own.
func TestLsAppliesTheFloorGrepApplies(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, granted := filepath.Join(base, "vega"), filepath.Join(base, "grants", "aii-os")
	data := filepath.Join(home, "data")
	lsTree(t, home, []string{"notes.md", "ledger.jsonl", "providers.json", "data/credentials/k.key", "data/ui-layout.json"})
	lsTree(t, granted, []string{"config.json", "internal/plain.go"})
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data,
		Except: []string{filepath.Join(data, "ui-layout.json")}, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	r.SetExtraRoots([]string{granted})

	for _, recursive := range []bool{false, true} {
		res := lsCall(t, r, map[string]any{"recursive": recursive})
		for _, kept := range []string{"ledger.jsonl", "providers.json", "credentials", "k.key"} {
			if strings.Contains(res.Output, kept) {
				t.Errorf("recursive=%v named %s, which the floor keeps:\n%s", recursive, kept, res.Output)
			}
		}
		if !strings.Contains(res.Output, "protected by the substrate floor were not listed") || !strings.Contains(res.Output, "notes.md") {
			t.Errorf("recursive=%v: the floor's omissions must be counted:\n%s", recursive, res.Output)
		}
		// What the floor leaves open inside a kept place is listed, as
		// read opens it.
		if open := strings.Contains(res.Output, filepath.Join("data", "ui-layout.json")); open != recursive {
			t.Errorf("recursive=%v: the kept place's exception listed: %v\n%s", recursive, open, res.Output)
		}
	}
	res := lsCall(t, r, map[string]any{"path": granted, "recursive": true})
	if got := strings.Join(names(res.Output), " "); got != "config.json internal/ internal/plain.go" {
		t.Errorf("a granted root's own names are listed: %s\n%s", got, res.Output)
	}
}
