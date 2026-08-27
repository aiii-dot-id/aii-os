package tools

// grep_purego.go — platform-independent grep used by ALL platforms.
// The old implementation exec'd the grep(1) BINARY; that was a Linux
// habit with no portability story (assessed 2026-08-17: R1). This walks
// files with Go's regexp and filepath — identical output contract.

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// grepResult is one match line.
type grepResult struct {
	path    string
	lineNum int
	text    string
}

// grepWalk searches pattern across the tree rooted at root, skipping
// binary files (NUL-byte sniff on the first block) and honoring max
// results to bound output.
// deny, when set, refuses a path the substrate floor protects. Walking a
// tree and opening every file is a READ of every file: refusing `read
// providers.json` while `grep` happily prints its contents is not a
// boundary, it is a speed bump.
func grepWalk(ctx context.Context, pattern, root string, maxResults int, deny func(string) bool) ([]grepResult, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	var results []grepResult
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries skip, not fail
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(results) >= maxResults {
			return filepath.SkipAll
		}
		select {
		case <-ctx.Done():
			return filepath.SkipAll
		default:
		}

		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if deny != nil && deny(path) {
			return nil // protected by the substrate floor — not searchable
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		// binary sniff: skip files with NUL in the first 1KB
		probe := make([]byte, 1024)
		n, _ := f.Read(probe)
		for i := 0; i < n; i++ {
			if probe[i] == 0 {
				return nil // binary
			}
		}
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if re.MatchString(scanner.Text()) {
				results = append(results, grepResult{path: path, lineNum: lineNum, text: scanner.Text()})
				if len(results) >= maxResults {
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		return results, err
	}
	return results, nil
}

// dialectHint speaks up when a pattern found nothing AND is written in a
// dialect this engine does not read.
//
// grep_purego.go replaced the grep(1) BINARY to gain a portability story,
// and its header claims an "identical output contract". The output shape
// is identical; the PATTERN LANGUAGE is not. grep(1) defaults to POSIX
// BRE, where \| alternates and \( groups. Go's regexp is RE2, where both
// are escaped literals — so a BRE pattern compiles without error and
// silently matches nothing. The identity writes BRE constantly, because
// it writes it to bash all day, where it is correct.
//
// Silent + plausible + wrong is the worst answer a tool can give, so the
// no-match reply names the possibility. It is a HINT, not a diagnosis:
// a literal pipe is a legitimate thing to search for, which is why this
// only speaks when the search also came back empty.
func dialectHint(pattern string) string {
	for _, bre := range []string{`\|`, `\(`, `\)`, `\{`, `\}`, `\+`, `\?`} {
		if strings.Contains(pattern, bre) {
			return "\n[this engine is Go RE2, not grep(1) BRE: " + bre +
				" matches a literal " + strings.TrimPrefix(bre, `\`) +
				" here. Alternation is a|b, grouping is (a), one-or-more is a+ — all unescaped.]"
		}
	}
	return ""
}

// GrepTool Execute — the portable implementation (all platforms).
func (t *GrepTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	if pattern == "" {
		return Result{Error: "pattern is required"}, nil
	}

	const maxResults = 500
	results, err := grepWalk(ctx, pattern, path, maxResults, t.deny)
	if err != nil && len(results) == 0 {
		return Result{Error: err.Error()}, nil
	}
	if len(results) == 0 {
		// A search that found nothing SUCCEEDED. Returned as an Error it
		// was indistinguishable from a broken tool, and an identity that
		// cannot trust a negative has to confirm every one of them by
		// another route — which is what happened: shell greps re-run to
		// check the grep tool, two calls spent per null against a
		// bounded round budget.
		return Result{Output: "no matches for " + pattern + " under " + path + dialectHint(pattern)}, nil
	}

	var sb strings.Builder
	truncated := false
	for _, r := range results {
		line := fmt.Sprintf("%s:%d:%s\n", r.path, r.lineNum, r.text)
		if sb.Len()+len(line) > 51200 {
			truncated = true
			break
		}
		sb.WriteString(line)
	}
	out := sb.String()
	if truncated {
		out += fmt.Sprintf("…[truncated at %d of %d+ matches]\n", len(results), len(results))
	}
	return Result{Output: out}, nil
}
