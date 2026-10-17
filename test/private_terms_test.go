package test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type privateTerm struct {
	n     int    // 1-based, in the file's order: what a report names
	kind  string // "word" or "sub"
	value string
}

type privateTerms struct {
	terms []privateTerm
	allow map[string]map[string]bool // path -> term value -> approved
}

// parsePrivateTerms reads the terms file's text. An error names a line by
// its number only.
func parsePrivateTerms(src string) (*privateTerms, error) {
	p := &privateTerms{allow: map[string]map[string]bool{}}
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, rest, ok := strings.Cut(line, ":")
		if !ok || rest == "" {
			return nil, fmt.Errorf("terms file line %d: no kind, or nothing after it", i+1)
		}
		switch kind {
		case "word", "sub":
			p.terms = append(p.terms, privateTerm{n: len(p.terms) + 1, kind: kind, value: rest})
		case "allow":
			path, term, ok := strings.Cut(rest, ":")
			if !ok || path == "" || term == "" {
				return nil, fmt.Errorf("terms file line %d: an allow line is allow:<path>:<term>", i+1)
			}
			if p.allow[path] == nil {
				p.allow[path] = map[string]bool{}
			}
			p.allow[path][term] = true
		default:
			return nil, fmt.Errorf("terms file line %d: unknown kind (word, sub or allow)", i+1)
		}
	}
	if len(p.terms) == 0 {
		return nil, fmt.Errorf("the terms file lists no term")
	}
	return p, nil
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// holds reports whether line holds the term.
func (t privateTerm) holds(line string) bool {
	if t.kind == "sub" {
		return strings.Contains(line, t.value)
	}
	for from := 0; ; {
		at := strings.Index(line[from:], t.value)
		if at < 0 {
			return false
		}
		at += from
		end := at + len(t.value)
		if (at == 0 || !isWordByte(line[at-1])) && (end == len(line) || !isWordByte(line[end])) {
			return true
		}
		from = at + 1
	}
}

// scan returns one finding per line and term in content, the term named
// by number and kind only. An allow line for this path admits its term,
// whichever kind it is spelled with.
func (p *privateTerms) scan(path string, content []byte) []string {
	var found []string
	for i, line := range strings.Split(string(content), "\n") {
		for _, t := range p.terms {
			if !t.holds(line) || p.allow[path][t.value] || p.allow[path][t.kind+":"+t.value] {
				continue
			}
			found = append(found, fmt.Sprintf("%s:%d contains private term #%d (%s)", path, i+1, t.n, t.kind))
		}
	}
	return found
}

// guard:tree
func TestNoPrivateTermInTheTree(t *testing.T) {
	file := os.Getenv("AII_PRIVATE_TERMS")
	if file == "" {
		t.Skip("AII_PRIVATE_TERMS names no terms file — a public build has none; the gate sets it")
	}
	src, err := os.ReadFile(file)
	if err != nil {
		t.Skipf("AII_PRIVATE_TERMS names a file this run cannot read (%v) — a public build has none; the gate sets it", err)
	}
	terms, err := parsePrivateTerms(string(src))
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	cmd := exec.Command("git", "-c", "core.quotePath=off", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v — the terms are set, so the tree must be read", err)
	}
	checked := 0
	seen := map[string]bool{}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || seen[rel] || strings.HasPrefix(rel, "docs/") {
			continue
		}
		seen[rel] = true
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				continue // staged, then deleted: nothing to read
			}
			t.Fatal(err)
		}
		head := b
		if len(head) > 8000 {
			head = head[:8000]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			continue // binary, by git's own test
		}
		checked++
		for _, f := range terms.scan(rel, b) {
			t.Error(f)
		}
	}
	if checked < 1000 {
		t.Fatalf("checked %d files — the walk is not reading the tree", checked)
	}
}

// guard:tree
func TestThePrivateTermsGuardRefusesWhatItIsFor(t *testing.T) {
	// The terms are fictional, made for this test; no real one is written
	// into the tree to prove the tree refuses it.
	terms, err := parsePrivateTerms("# fictional\nword:Zorvath\n\nsub:/srv/never-real/\n")
	if err != nil {
		t.Fatal(err)
	}
	file := []byte("package x\n\n// asked of Zorvath on Tuesday\nconst p = \"/srv/never-real/data\"\n" +
		"// Zorvaths, zorvath and XZorvath are other words\n")
	got := terms.scan("internal/x/x.go", file)
	want := []string{
		"internal/x/x.go:3 contains private term #1 (word)",
		"internal/x/x.go:4 contains private term #2 (sub)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, f := range got {
		if strings.Contains(f, "Zorvath") || strings.Contains(f, "never-real") {
			t.Fatalf("a finding echoes the term it refuses: %s", f)
		}
	}

	// An allow line admits its term in its path, and nowhere else.
	allowed, err := parsePrivateTerms("word:Zorvath\nsub:/srv/never-real/\nallow:internal/x/x.go:Zorvath\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := allowed.scan("internal/x/x.go", file); len(got) != 1 || got[0] != want[1] {
		t.Fatalf("the allow line did not admit exactly its term: %q", got)
	}
	if got := allowed.scan("internal/y/y.go", file); len(got) != 2 {
		t.Fatalf("an allow line for one path admitted another: %q", got)
	}

	// A line the guard cannot read is refused, and named by number alone.
	for _, bad := range []string{"name:Zorvath\n", "word:\n", "allow:internal/x/x.go\n", "# only a comment\n"} {
		if _, err := parsePrivateTerms(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		} else if strings.Contains(err.Error(), "Zorvath") {
			t.Errorf("the refusal echoes the term: %v", err)
		}
	}
}
