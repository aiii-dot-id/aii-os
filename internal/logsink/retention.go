package logsink

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// .
// .
// .
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
	sort.Strings(names)
	return names, nil
}

// .
// .
// .
// .
func rotatedAt(name string) (time.Time, bool) {
	stamp := strings.TrimPrefix(name, rotatedPrefix)
	stamp = strings.TrimSuffix(stamp, gzipExt)
	stamp = strings.TrimSuffix(stamp, rotatedExt)
	at, err := time.Parse("20060102-150405", stamp)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// .
// .
// .
func (s *Sink) Prune() (int, error) {
	keep := s.cfg.maxBackups()
	days := s.cfg.maxDays()
	if keep < 0 {
		return 0, nil
	}
	names, err := s.rotated()
	if err != nil {
		return 0, err
	}
	// .
	// .
	// .
	// .
	// .
	var floor time.Time
	if days > 0 {
		floor = time.Now().AddDate(0, 0, -days)
	}
	removed := 0
	for len(names) > keep {
		oldest := names[0]
		if days > 0 {
			// .
			// .
			if at, ok := rotatedAt(oldest); ok && at.After(floor) {
				break
			}
		}
		if err := os.Remove(filepath.Join(s.dir, oldest)); err != nil {
			return removed, err
		}
		names = names[1:]
		removed++
	}
	return removed, nil
}
