package untrusted

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The package's founding claim, rendered as enforcement: "there were TWO
// implementations of that invariant and they disagreed ... a page
// containing the close marker escaped its own label" (package doc). The
// cure was one owner. This test keeps it true: any non-test file in the
// tree that hardcodes either sentinel has become a second owner — wrap
// through untrusted.Wrap, or reference the exported constants.
//
// Mirrors internal/hostcap's exec-site census (execguard_test.go): a
// tree scan with an explicit allowlist, so a new site fails the test
// until its author has made the ownership decision consciously.
//
// guard:tree
func TestSentinelLiteralsHaveOneOwner(t *testing.T) {
	root := "../.."
	// Escaped so this file's own pattern text is not itself a sentinel.
	sentinel := regexp.MustCompile(`\[\[\[EXTERNAL_UNTRUSTED_CONTENT\]\]\]|\[\[\[END_EXTERNAL_UNTRUSTED_CONTENT\]\]\]`)

	// file (repo-relative) -> why a literal there is not a second owner.
	allowlist := map[string]string{
		"internal/untrusted/untrusted.go": "the owning package: the constants themselves",
	}

	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".gopath", ".git", "attic", "testdata", "worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // prose about the sentinel is not a second implementation
			}
			if sentinel.MatchString(line) {
				if _, ok := allowlist[rel]; !ok {
					violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("sentinel literal(s) outside untrusted — a second owner of the wrap invariant is how the two-disagreement defect was born (package doc). Wrap via untrusted.Wrap or use the untrusted.Open/Close constants:\n  %s",
			strings.Join(violations, "\n  "))
	}
	for rel := range allowlist {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("allowlist names %s, which no longer exists — prune it", rel)
		}
	}
}
