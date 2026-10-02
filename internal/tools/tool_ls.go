package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type LsTool struct {
	deny func(path string) bool
}

func (t *LsTool) Name() string { return "ls" }
func (t *LsTool) Description() string {
	return "List a directory: type, modified time (UTC), size, name. recursive skips .git and node_modules."
}

func (t *LsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":      map[string]interface{}{"type": "string", "description": "Directory or file (default: your home)"},
			"recursive": map[string]interface{}{"type": "boolean"},
			"pattern":   map[string]interface{}{"type": "string", "description": "Glob on names: *.go"},
			"depth":     map[string]interface{}{"type": "integer"},
			"sort":      map[string]interface{}{"type": "string", "enum": []string{"name", "mtime"}, "description": "mtime: newest first"},
			"limit":     map[string]interface{}{"type": "integer"},
			"offset":    map[string]interface{}{"type": "integer"},
		},
	}
}

type lsQuery struct {
	recursive bool
	pattern   string
	depth     int
	byTime    bool
	offset    int
	limit     int
}

func lsQueryOf(args map[string]interface{}) (lsQuery, error) {
	var q lsQuery
	var err error
	if q.recursive, err = boolArg(args, "recursive"); err != nil {
		return q, err
	}
	if q.pattern, err = stringArg(args, "pattern"); err != nil {
		return q, err
	}
	if q.pattern != "" {
		if err := globError("pattern", q.pattern); err != nil {
			return q, err
		}
	}
	if q.depth, err = intArg(args, "depth", 0); err != nil {
		return q, fmt.Errorf("depth: %w", err)
	}
	switch {
	case q.depth < 0:
		return q, fmt.Errorf("depth must not be negative, got %d", q.depth)
	case q.depth > 0 && !q.recursive:
		return q, fmt.Errorf("depth bounds a recursive listing; pass recursive=true with it")
	case !q.recursive:
		q.depth = 1
	}
	order, err := stringArg(args, "sort")
	if err != nil {
		return q, err
	}
	switch order {
	case "", "name":
	case "mtime":
		q.byTime = true
	default:
		return q, fmt.Errorf("sort must be name or mtime, got %q", order)
	}
	q.offset, q.limit, err = pageArgs(args)
	return q, err
}

type lsEntry struct {
	rel  string
	path string
	info fs.FileInfo
	at   int
}

type lsWalk struct {
	walkCoverage
	total int
	kept  []lsEntry
}

func (t *LsTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	path, _ := args["path"].(string)
	q, err := lsQueryOf(args)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	l := lsWalk{walkCoverage: walkCoverage{depth: q.depth}}
	if err := l.walk(ctx, q, root, t.deny); err != nil {
		return Result{Error: err.Error()}, nil
	}
	if q.byTime {
		l.newestFirst()
		l.kept = l.kept[min(q.offset, len(l.kept)):min(q.offset+q.limit, len(l.kept))]
	}
	if l.total == 0 {
		out := "no entries under " + path
		if q.pattern != "" {
			out += " matching pattern=" + q.pattern
		}
		return Result{Output: out + coverageNote(l.walkCoverage, "listed"), Truncated: l.skipped > 0 || l.cancelled}, nil
	}
	var page strings.Builder
	shown, full := 0, false
	for _, e := range l.kept {
		line := lsLine(e)
		if shown > 0 && page.Len()+len(line) > pageBytes {
			full = true
			break
		}
		page.WriteString(line)
		shown++
	}
	more := q.offset+shown < l.total
	out := page.String() + pageNote(q.offset, shown, l.total, counted(l.total, "entry", "entries"), l.cancelled, full)
	return Result{Output: out + coverageNote(l.walkCoverage, "listed"),
		Truncated: more || full || l.skipped > 0 || l.cancelled}, nil
}

func (l *lsWalk) walk(ctx context.Context, q lsQuery, root string, deny func(string) bool) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			l.skipped++
			return nil
		}
		if path == root && d.IsDir() {
			return nil
		}
		select {
		case <-ctx.Done():
			l.cancelled = true
			return filepath.SkipAll
		default:
		}
		rel := walkRel(root, path)
		if deny != nil && deny(path) {

			l.denied++
		} else if q.pattern == "" || globMatch(q.pattern, rel) {
			l.add(q, rel, path, d)
		}
		if !d.IsDir() {
			return nil
		}
		switch {
		case !q.recursive:
			return filepath.SkipDir
		case skipTree(path, root, d):
			l.excluded++
			return filepath.SkipDir
		case q.depth > 0 && strings.Count(filepath.ToSlash(rel), "/")+1 >= q.depth:
			l.cut++
			return filepath.SkipDir
		}
		return nil
	})
}

func (l *lsWalk) add(q lsQuery, rel, path string, d fs.DirEntry) {
	at := l.total
	if !q.byTime && (at < q.offset || at >= q.offset+q.limit) {
		l.total++
		return
	}
	info, err := d.Info()
	if err != nil {
		l.skipped++
		return
	}
	l.total++
	l.kept = append(l.kept, lsEntry{rel, path, info, at})

	if keep := q.offset + q.limit; q.byTime && len(l.kept) >= 2*keep+64 {
		l.newestFirst()
		l.kept = l.kept[:keep]
	}
}

func (l *lsWalk) newestFirst() {
	sort.Slice(l.kept, func(i, j int) bool {
		a, b := l.kept[i], l.kept[j]
		if !a.info.ModTime().Equal(b.info.ModTime()) {
			return a.info.ModTime().After(b.info.ModTime())
		}
		return a.at < b.at
	})
}

func lsLine(e lsEntry) string {
	mode := e.info.Mode()
	kind, size, name := "o", "-", e.rel
	switch {
	case mode.IsDir():
		kind, name = "d", name+string(filepath.Separator)
	case mode&fs.ModeSymlink != 0:
		kind = "l"
		if target, err := os.Readlink(e.path); err == nil {
			name += " -> " + target
		}
	case mode.IsRegular():
		kind, size = "f", strconv.FormatInt(e.info.Size(), 10)
	}
	return kind + "  " + e.info.ModTime().UTC().Format(time.RFC3339) + "  " + size + "  " + name + "\n"
}

func (t *LsTool) ReadOnly() bool { return true }
