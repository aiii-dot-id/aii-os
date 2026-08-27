package logsink

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rotated returns the rotated log names (compressed or not), oldest
// first. Name order IS age order by construction (the timestamp
// prefix sorts), so no mtime arithmetic decides eviction.
func (s *Sink) rotated() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, rotatedPrefix) {
			continue
		}
		if strings.HasSuffix(n, rotatedExt) || strings.HasSuffix(n, rotatedExt+gzipExt) {
			names = append(names, n)
		}
	}
	sort.Strings(names) // oldest first
	return names, nil
}

// Prune evicts the oldest rotated logs beyond MaxBackups. The live
// log is never a candidate. Returns how many were removed.
func (s *Sink) Prune() (int, error) {
	keep := s.cfg.maxBackups()
	if keep < 0 {
		return 0, nil
	}
	names, err := s.rotated()
	if err != nil {
		return 0, err
	}
	removed := 0
	for len(names) > keep {
		oldest := names[0]
		if err := os.Remove(filepath.Join(s.dir, oldest)); err != nil {
			return removed, err
		}
		names = names[1:]
		removed++
	}
	return removed, nil
}
