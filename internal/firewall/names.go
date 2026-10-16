package firewall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

type fileID struct{ volume, index uint64 }

type inside struct {
	at     map[fileID][]string
	cannot []unchecked
}

var walks atomic.Int64

type look struct {
	once sync.Once
	in   *inside
}

func (p *Policy) Look() *Policy {
	if p.links != nil {
		return p
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return &Policy{rules: slices.Clone(p.rules), gone: slices.Clone(p.gone), links: &look{}}
}

func (p *Policy) inside() *inside {
	if p.links == nil {
		return p.walkInside()
	}
	p.links.once.Do(func() { p.links.in = p.walkInside() })
	return p.links.in
}

func (p *Policy) walkInside() *inside {
	walks.Add(1)
	p.mu.RLock()
	var places []*Rule
	for _, r := range p.rules {
		if r.Enforced && r.Kind == KindSubstrate && r.Path != "" && !r.Linkless {
			places = append(places, r)
		}
	}
	p.mu.RUnlock()
	in := &inside{at: map[fileID][]string{}}
	for _, place := range places {
		roots := []string{place.Path}
		if place.Stem {

			var err error
			if roots, err = stemEntries(place); err != nil {
				in.cannot = append(in.cannot, unchecked{place, err, stopOf(filepath.Dir(place.Path))})
				continue
			}
		}
		for _, root := range roots {
			in.walk(place, root)
		}
	}
	return in
}

func (in *inside) walk(place *Rule, root string) {
	info, err := os.Stat(root)
	if err != nil {
		if !notMade(err) {
			in.cannot = append(in.cannot, unchecked{place, err, stopOf(root)})
		}
		return
	}
	if !info.IsDir() && !place.Stem {
		return
	}
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !covers(place, canonical(path)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err == nil && !d.Type().IsRegular() {
			return nil
		}
		var id fileID
		if err == nil {
			id, err = entryID(path, d)
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			in.cannot = append(in.cannot, unchecked{place, err, Stop{Path: path}})
			return filepath.SkipAll
		}
		in.at[id] = append(in.at[id], path)
		return nil
	})
}

func (p *Policy) secondName(id fileID, names uint64) (string, *Rule) {
	if names < 2 {
		return "", nil
	}
	in := p.inside()
	var cannot []unchecked
	for _, c := range in.cannot {
		if !p.isGone(c.place.Path) && !p.isGone(c.stop.Path) {
			cannot = append(cannot, c)
		}
	}
	if len(cannot) > 0 {
		return "", unwalked(cannot)
	}
	for _, at := range in.at[id] {
		if !p.isGone(at) {
			return at, nil
		}
	}
	return "", nil
}

func Names(file os.FileInfo, by *os.File) uint64 {
	_, names, ok := namesOf(file, by)
	if !ok {
		return 0
	}
	return names
}

func unwalked(cannot []unchecked) *Rule {
	rule := unasked(cannot)
	rule.Stops = nil
	var stopped, errs []string
	for _, c := range cannot {
		err := c.err
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}

		from := c.place.Path
		if c.place.Stem {
			from = filepath.Dir(from)
		}
		at := filepath.Base(from)
		if rel, rerr := filepath.Rel(from, c.stop.Path); rerr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			at = filepath.Join(at, rel)
		}
		if !slices.Contains(stopped, at) {
			stopped = append(stopped, at)
		}
		if e := err.Error(); !slices.Contains(errs, e) {
			errs = append(errs, e)
		}
	}
	where := strings.Join(stopped, ", ")
	rule.Reason = fmt.Sprintf("the host could not read %s, in a place it keeps (%s), so it cannot tell whether a file with more than one name is one of the files there, and refuses every such file until your operator makes %s readable to the host (removing it instead is the operator's own choice)", where, strings.Join(errs, ", "), where)
	return rule
}
