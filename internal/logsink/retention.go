package logsink

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const rotatedStampLayout = "20060102-150405"

func isRotatedName(n string) bool {
	return strings.HasPrefix(n, rotatedPrefix) &&
		(strings.HasSuffix(n, rotatedExt) || strings.HasSuffix(n, rotatedExt+gzipExt))
}

func rotatedStamp(name string) (stamp string, seq int) {
	stamp = strings.TrimPrefix(name, rotatedPrefix)
	stamp = strings.TrimSuffix(stamp, gzipExt)
	stamp = strings.TrimSuffix(stamp, rotatedExt)
	if at := len(rotatedStampLayout); len(stamp) > at+1 && stamp[at] == '-' {
		if n, err := strconv.Atoi(stamp[at+1:]); err == nil && n > 0 {
			return stamp[:at], n
		}
	}
	return stamp, 0
}

func (s *Sink) rotated() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if isRotatedName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		si, ni := rotatedStamp(names[i])
		sj, nj := rotatedStamp(names[j])
		if si != sj {
			return si < sj
		}
		if ni != nj {
			return ni < nj
		}
		return names[i] < names[j]
	})
	return names, nil
}

func rotatedAt(name string) (time.Time, bool) {
	stamp, _ := rotatedStamp(name)
	at, err := time.Parse(rotatedStampLayout, stamp)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

func (s *Sink) Prune() (int, error) {
	removed, _, err := s.prune()
	return removed, err
}

type totalEviction struct {
	files       int
	bytes, left int64
}

func (s *Sink) prune() (int, totalEviction, error) {
	keep := s.cfg.maxBackups()
	days := s.cfg.maxDays()

	if keep < 0 && s.cfg.MaxTotalBytes <= 0 {
		return 0, totalEviction{}, nil
	}
	names, err := s.rotated()
	if err != nil {
		return 0, totalEviction{}, err
	}

	var floor time.Time
	if days > 0 {
		floor = time.Now().AddDate(0, 0, -days)
	}
	removed := 0
	for keep >= 0 && len(names) > keep {
		oldest := names[0]
		if days > 0 {

			if at, ok := rotatedAt(oldest); ok && at.After(floor) {
				break
			}
		}
		if err := os.Remove(filepath.Join(s.dir, oldest)); err != nil {
			return removed, totalEviction{}, err
		}
		names = names[1:]
		removed++
	}

	sizes := make([]int64, len(names))
	var total int64
	for i, n := range names {
		if fi, err := os.Lstat(filepath.Join(s.dir, n)); err == nil {
			sizes[i] = fi.Size()
			total += sizes[i]
		}
	}
	bound := s.cfg.maxTotalBytes()
	var cut totalEviction
	for len(names) > 0 && total > bound {
		if err := os.Remove(filepath.Join(s.dir, names[0])); err != nil {
			cut.left = total
			return removed + cut.files, cut, err
		}
		total -= sizes[0]
		cut.files++
		cut.bytes += sizes[0]
		names, sizes = names[1:], sizes[1:]
	}
	cut.left = total
	return removed + cut.files, cut, nil
}
