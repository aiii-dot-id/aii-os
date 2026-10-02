package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type grepQuery struct {
	re      *regexp.Regexp
	literal bool
	include string
	context int
	output  string
	offset  int
	limit   int
}

const (
	maxGrepContext = 100
	maxGrepLine    = 500
)

func grepQueryOf(args map[string]interface{}) (grepQuery, error) {
	var q grepQuery
	var err error
	pattern, _ := args["pattern"].(string)
	if q.literal, err = boolArg(args, "literal"); err != nil {
		return q, err
	}
	ignoreCase, err := boolArg(args, "ignore_case")
	if err != nil {
		return q, err
	}
	expr := pattern
	if q.literal {
		expr = regexp.QuoteMeta(pattern)
	}
	if ignoreCase {
		expr = "(?i)" + expr
	}
	if q.re, err = regexp.Compile(expr); err != nil {
		return q, fmt.Errorf("invalid pattern: %w", err)
	}
	if q.include, err = stringArg(args, "include"); err != nil {
		return q, err
	}
	if q.include != "" {
		if err := globError("include", q.include); err != nil {
			return q, err
		}
	}
	if q.context, err = intArg(args, "context", 0); err != nil {
		return q, fmt.Errorf("context: %w", err)
	}
	if q.context < 0 || q.context > maxGrepContext {
		return q, fmt.Errorf("context must be between 0 and %d lines, got %d", maxGrepContext, q.context)
	}
	if q.output, err = stringArg(args, "output"); err != nil {
		return q, err
	}
	switch q.output {
	case "":
		q.output = "content"
	case "content", "files", "count":
	default:
		return q, fmt.Errorf("output must be content, files or count, got %q", q.output)
	}
	q.offset, q.limit, err = pageArgs(args)
	return q, err
}

type walkCoverage struct {
	skipped   int
	excluded  int
	binary    int
	denied    int
	cancelled bool
	cut       int
	depth     int
}

type grepScan struct {
	walkCoverage
	partial  bool
	matches  int
	files    int
	included int
	page     grepPage
}

type grepPage struct {
	out      strings.Builder
	shown    int
	full     bool
	clipped  bool
	lastPath string
	lastLine int
}

type grepLine struct {
	num     int
	text    string
	clipped bool
}

func grepWalk(ctx context.Context, q grepQuery, root string, deny func(string) bool) grepScan {
	var s grepScan
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.skipped++
			return nil
		}
		if d.IsDir() {
			if skipTree(path, root, d) {

				s.excluded++
				return filepath.SkipDir
			}
			return nil
		}
		select {
		case <-ctx.Done():

			s.cancelled = true
			return filepath.SkipAll
		default:
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if q.include != "" {
			if !globMatch(q.include, walkRel(root, path)) {
				return nil
			}
			s.included++
		}
		if deny != nil && deny(path) {

			s.denied++
			return nil
		}
		s.file(q, path)
		return nil
	})
	return s
}

func skipTree(path, root string, d fs.DirEntry) bool {
	name := d.Name()
	return path != root && (name == ".git" || name == "node_modules")
}

func walkRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return filepath.Base(path)
	}
	return rel
}

func (s *grepScan) file(q grepQuery, path string) {
	f, err := os.Open(path)
	if err != nil {
		s.skipped++
		return
	}
	defer f.Close()

	probe := make([]byte, 1024)
	n, _ := f.Read(probe)
	if bytes.IndexByte(probe[:n], 0) >= 0 {

		s.binary++
		return
	}
	if _, err := f.Seek(0, 0); err != nil {
		s.skipped++
		return
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var held []grepLine
	num, found, after := 0, 0, 0
	content := q.output == "content"
	for scanner.Scan() {
		num++
		line := scanner.Bytes()
		if q.re.Match(line) {
			k := s.matches
			s.matches++
			found++
			if q.output == "files" {
				break
			}
			if content && s.page.wants(q, k) {
				s.page.match(q, path, num, line, held)
				after = q.context
			} else if k >= q.offset+q.limit {
				after = 0
			}
		} else if after > 0 {
			s.page.context(path, num, line)
			after--
		}
		if q.context > 0 && content && !s.page.full && s.matches < q.offset+q.limit {
			text, clipped := shownLine(line, nil, num)
			if held = append(held, grepLine{num, text, clipped}); len(held) > q.context {
				held = held[1:]
			}
		}
	}

	if scanner.Err() != nil {
		s.partial = true
	}
	if found == 0 {
		return
	}
	k := s.files
	s.files++
	if !content && s.page.wants(q, k) {
		entry := path
		if q.output == "count" {
			entry += ":" + strconv.Itoa(found)
		}
		if s.page.add(entry + "\n") {
			s.page.shown++
		}
	}
}

func (p *grepPage) wants(q grepQuery, k int) bool {
	return !p.full && k >= q.offset && k < q.offset+q.limit
}

func (p *grepPage) add(line string) bool {
	if p.out.Len()+len(line) > pageBytes {
		p.full = true
		return false
	}
	p.out.WriteString(line)
	return true
}

func (p *grepPage) match(q grepQuery, path string, num int, line []byte, held []grepLine) {
	group := make([]grepLine, 0, len(held)+1)
	for _, h := range held {
		if path != p.lastPath || h.num > p.lastLine {
			group = append(group, h)
		}
	}
	text, clipped := shownLine(line, q.re, num)
	group = append(group, grepLine{num, text, clipped})
	render := func() string {
		var b strings.Builder
		if q.context > 0 && p.out.Len() > 0 && (path != p.lastPath || group[0].num > p.lastLine+1) {
			b.WriteString("--\n")
		}
		for i, g := range group {
			sep := "-"
			if i == len(group)-1 {
				sep = ":"
			}
			fmt.Fprintf(&b, "%s%s%d%s%s\n", path, sep, g.num, sep, g.text)
		}
		return b.String()
	}
	out := render()
	for p.out.Len()+len(out) > pageBytes {
		if p.shown > 0 {
			p.full = true
			return
		}
		if len(group) == 1 {
			break
		}
		group = group[1:]
		out = render()
	}
	p.out.WriteString(out)
	for _, g := range group {
		p.clipped = p.clipped || g.clipped
	}
	p.shown++
	p.lastPath, p.lastLine = path, num
}

func (p *grepPage) context(path string, num int, line []byte) {
	if p.full {
		return
	}
	text, clipped := shownLine(line, nil, num)
	if p.add(fmt.Sprintf("%s-%d-%s\n", path, num, text)) {
		p.clipped = p.clipped || clipped
		p.lastPath, p.lastLine = path, num
	}
}

func shownLine(line []byte, re *regexp.Regexp, num int) (string, bool) {
	if len(line) <= maxGrepLine {
		return string(line), false
	}
	start := 0
	if re != nil {
		if loc := re.FindIndex(line); loc != nil {
			start = max(0, min(loc[0]-maxGrepLine/5, len(line)-maxGrepLine))
		}
	}
	for start > 0 && !utf8.RuneStart(line[start]) {
		start--
	}
	end := min(start+maxGrepLine, len(line))
	for end < len(line) && !utf8.RuneStart(line[end]) {
		end--
	}
	text := string(line[start:end])
	if start > 0 {
		text = "…" + text
	}
	return text + fmt.Sprintf("…[clipped: line %d is %d bytes; read offset=%d shows it]", num, len(line), num), true
}

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

func (t *GrepTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	if pattern == "" {
		return Refusal(ReasonArgumentsRequired, "pattern is required"), nil
	}
	q, err := grepQueryOf(args)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	scan := grepWalk(ctx, q, path, t.deny)
	trouble := scan.partial || scan.cancelled || scan.skipped > 0
	if scan.matches == 0 {

		out := "no matches for " + pattern + " under " + path
		if q.include != "" {

			out += " in " + counted(scan.included, "file", "files") + " matching include=" + q.include
		}
		if n := scan.skipped; n > 0 {
			out += fmt.Sprintf(" (%d path(s) could not be read)", n)
		}
		if scan.partial {
			out += " (a file could not be scanned to the end)"
		}
		hint := ""
		if !q.literal {
			hint = dialectHint(pattern)
		}
		return Result{Output: out + coverageNote(scan.walkCoverage, "searched") + hint, Truncated: trouble}, nil
	}

	count := scan.matches
	total := counted(scan.matches, "match", "matches") + " in " + counted(scan.files, "file", "files")
	if q.output != "content" {
		count, total = scan.files, counted(scan.files, "file", "files")+" with matches"
		if q.output == "count" {
			total += " (" + counted(scan.matches, "match", "matches") + ")"
		}
	}
	out := scan.page.out.String() + pageNote(q.offset, scan.page.shown, count, total, scan.cancelled, scan.page.full)
	if scan.skipped > 0 {
		out += fmt.Sprintf("…[%d path(s) could not be read]\n", scan.skipped)
	}
	if scan.partial {
		out += "…[a file could not be scanned to the end]\n"
	}
	if scan.cancelled {
		out += "…[the search was cancelled before the tree was fully walked]\n"
	}

	more := q.offset+scan.page.shown < count
	return Result{Output: out + coverageNote(scan.walkCoverage, "searched"),
		Truncated: more || scan.page.full || scan.page.clipped || trouble}, nil
}

func coverageNote(c walkCoverage, verb string) string {
	var why []string
	if c.cancelled {
		why = append(why, "the walk was cancelled before the tree was fully walked")
	}
	if c.excluded > 0 {
		why = append(why, fmt.Sprintf("%d directory tree(s) skipped by policy (.git, node_modules)", c.excluded))
	}
	if c.binary > 0 {
		why = append(why, fmt.Sprintf("%d binary file(s) not %s", c.binary, verb))
	}
	if c.skipped > 0 {
		why = append(why, fmt.Sprintf("%d path(s) could not be read", c.skipped))
	}
	if c.denied > 0 {
		why = append(why, fmt.Sprintf("%d path(s) protected by the substrate floor were not %s", c.denied, verb))
	}
	if c.cut > 0 {
		why = append(why, fmt.Sprintf("%d directory(ies) at depth %d not entered; a larger depth lists what they hold", c.cut, c.depth))
	}
	if len(why) == 0 {
		return ""
	}
	return "\n[coverage: " + strings.Join(why, "; ") + "]"
}
