package tools

// .
// .
// .
// .

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

// .
type grepResult struct {
	path    string
	lineNum int
	text    string
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
// .
// .
// .
// .
// .
// .
type grepScan struct {
	results   []grepResult
	capped    bool
	skipped   int
	partial   bool
	excluded  int
	binary    int
	denied    int
	cancelled bool
}

func grepWalk(ctx context.Context, pattern, root string, maxResults int, deny func(string) bool) (grepScan, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return grepScan{}, fmt.Errorf("invalid pattern: %w", err)
	}

	var scan grepScan
	results := scan.results
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			scan.skipped++
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				// .
				// .
				// .
				// .
				scan.excluded++
				return filepath.SkipDir
			}
			return nil
		}
		if len(results) >= maxResults {
			scan.capped = true
			return filepath.SkipAll
		}
		select {
		case <-ctx.Done():
			// .
			// .
			// .
			// .
			scan.cancelled = true
			return filepath.SkipAll
		default:
		}

		info, err := d.Info()
		if err != nil {
			scan.skipped++
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if deny != nil && deny(path) {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			scan.denied++
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			scan.skipped++
			return nil
		}
		defer f.Close()

		// .
		probe := make([]byte, 1024)
		n, _ := f.Read(probe)
		for i := 0; i < n; i++ {
			if probe[i] == 0 {
				// .
				// .
				// .
				// .
				// .
				scan.binary++
				return nil
			}
		}
		if _, err := f.Seek(0, 0); err != nil {
			scan.skipped++
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
					scan.capped = true
					return nil
				}
			}
		}
		// .
		// .
		if err := scanner.Err(); err != nil {
			scan.partial = true
		}
		return nil
	})
	scan.results = results
	if err != nil {
		return scan, err
	}
	return scan, nil
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
// .
// .
// .
// .
// .
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

// .
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
	scan, err := grepWalk(ctx, pattern, path, maxResults, t.deny)
	results := scan.results
	if err != nil && len(results) == 0 {
		return Result{Error: err.Error()}, nil
	}
	if len(results) == 0 {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		out := "no matches for " + pattern + " under " + path
		if n := scan.skipped; n > 0 {
			out += fmt.Sprintf(" (%d path(s) could not be read)", n)
		}
		if scan.partial {
			out += " (a file could not be scanned to the end)"
		}
		return Result{Output: out + coverageNote(scan) + dialectHint(pattern), Truncated: scan.partial || scan.cancelled || scan.skipped > 0}, nil
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
	shown := strings.Count(out, "\n")
	// .
	// .
	// .
	// .
	// .
	if truncated {
		out += fmt.Sprintf("…[%d of %d collected matches shown — output byte cap]\n", shown, len(results))
	}
	if scan.capped {
		out += fmt.Sprintf("…[collection stopped at %d matches — more may exist]\n", maxResults)
	}
	if scan.skipped > 0 {
		out += fmt.Sprintf("…[%d path(s) could not be read]\n", scan.skipped)
	}
	if scan.partial {
		out += "…[a file could not be scanned to the end]\n"
	}
	if scan.cancelled {
		out += "…[the search was cancelled before the tree was fully walked]\n"
	}
	return Result{Output: out + coverageNote(scan), Truncated: truncated || scan.capped || scan.partial || scan.cancelled || scan.skipped > 0}, nil
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
// .
// .
// .
// .
// .
// .
// .
func coverageNote(scan grepScan) string {
	var why []string
	if scan.cancelled {
		why = append(why, "the search was cancelled before the tree was fully walked")
	}
	if scan.excluded > 0 {
		why = append(why, fmt.Sprintf("%d directory tree(s) skipped by policy (.git, node_modules)", scan.excluded))
	}
	if scan.binary > 0 {
		why = append(why, fmt.Sprintf("%d binary file(s) not searched", scan.binary))
	}
	if scan.skipped > 0 {
		why = append(why, fmt.Sprintf("%d path(s) could not be read", scan.skipped))
	}
	if scan.denied > 0 {
		why = append(why, fmt.Sprintf("%d path(s) protected by the substrate floor were not searched", scan.denied))
	}
	if len(why) == 0 {
		return ""
	}
	return "\n[coverage: " + strings.Join(why, "; ") + "]"
}
