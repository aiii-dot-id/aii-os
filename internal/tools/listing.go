package tools

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	pageDefault = 100
	pageMax     = 500
	pageBytes   = 51200
)

func pageArgs(args map[string]interface{}) (offset, limit int, err error) {
	if offset, err = intArg(args, "offset", 0); err != nil {
		return 0, 0, fmt.Errorf("offset: %w", err)
	}
	if offset < 0 {
		return 0, 0, fmt.Errorf("offset must not be negative, got %d", offset)
	}
	if limit, err = intArg(args, "limit", 0); err != nil {
		return 0, 0, fmt.Errorf("limit: %w", err)
	}
	switch {
	case limit < 0:
		return 0, 0, fmt.Errorf("limit must not be negative, got %d", limit)
	case limit == 0:
		limit = pageDefault
	case limit > pageMax:
		limit = pageMax
	}
	return offset, limit, nil
}

func pageNote(offset, shown, count int, total string, atLeast, byteCap bool) string {
	end := offset + shown
	if offset == 0 && end >= count {
		return ""
	}
	if atLeast {
		total = "at least " + total
	}
	if offset >= count {
		return fmt.Sprintf("[offset=%d is past the end: %s]\n", offset, total)
	}
	note := fmt.Sprintf("[showing %d–%d of %s", offset+1, end, total)
	if byteCap {
		note += fmt.Sprintf(" (the page reached the %d KB output cap)", pageBytes/1024)
	}
	if end < count {
		note += fmt.Sprintf("; continue with offset=%d", end)
	}
	return note + "]\n"
}

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func globMatch(glob, rel string) bool {
	glob, rel = filepath.ToSlash(glob), filepath.ToSlash(rel)
	if !strings.Contains(glob, "/") {
		ok, _ := path.Match(glob, path.Base(rel))
		return ok
	}
	segs := strings.Split(rel, "/")

	reach := make([]bool, len(segs)+1)
	reach[0] = true
	for _, p := range strings.Split(glob, "/") {
		next := make([]bool, len(segs)+1)
		for j, ok := range reach {
			if !ok {
				continue
			}
			if p == "**" {
				for k := j; k <= len(segs); k++ {
					next[k] = true
				}
				break
			}
			if j < len(segs) {
				if m, _ := path.Match(p, segs[j]); m {
					next[j+1] = true
				}
			}
		}
		reach = next
	}
	return reach[len(segs)]
}

func globError(name, glob string) error {
	for _, p := range strings.Split(filepath.ToSlash(glob), "/") {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("%s %q is not a valid glob (* ? [a-z], and ** for any directories): %w", name, glob, err)
		}
	}
	return nil
}
