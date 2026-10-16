package ledger

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

func VerifyChainReading(path string, key func(first *Event) ([]byte, error), heads HeadVerifier,
	visit func(evt *Event, line []byte) error,
	rest func(container string, at int, line []byte, err error) error) (n int, cut int64, err error) {
	f, err := atomicfile.Open(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, 0, err
	}
	var end int64
	if f != nil {
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return 0, 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, 0, &os.PathError{Op: "read regular ledger", Path: path, Err: os.ErrInvalid}
		}
		if end, err = completeTailEnd(f, info.Size()); err != nil {
			return 0, 0, err
		}
		cut = info.Size() - end
	} else if segs, serr := segmentFiles(filepath.Dir(path)); serr != nil || len(segs) == 0 {

		return 0, 0, errors.Join(err, serr)
	}
	tail := func() io.Reader {
		if f == nil {
			return bytes.NewReader(nil)
		}
		return io.NewSectionReader(f, 0, end)
	}

	var lastContainer string
	var lastAt int
	refused := func(failure error) (int, int64, error) {
		if err := readRest(path, tail, lastContainer, lastAt, rest); err != nil {
			return 0, cut, err
		}
		return 0, cut, failure
	}

	var first *Event
	if err := stream(path, tail(), func(evt *Event) error {
		e := *evt
		first = &e
		return ErrStop
	}); err != nil {
		return refused(&VerifyFailure{Requirement: "read ledger: " + err.Error(), NextSeq: 1, Err: err})
	}
	pubKey, err := key(first)
	if err != nil {
		var failure *VerifyFailure
		if errors.As(err, &failure) {
			return refused(err)
		}
		return refused(&VerifyFailure{Requirement: err.Error(), Err: err})
	}

	var line []byte
	var at int
	n, err = verifyChain(func(fn func(*Event) error) error {
		return streamLines(path, tail(), func(evt *Event, l []byte, a int) error {
			line, at = l, a
			return fn(evt)
		})
	}, pubKey, heads, func(evt *Event) error {
		if err := visit(evt, line); err != nil {
			return err
		}
		lastContainer, lastAt = evt.container, at
		return nil
	})
	if err != nil {
		var failure *VerifyFailure
		if errors.As(err, &failure) {
			return refused(failure)
		}
		return 0, cut, err
	}
	return n, cut, nil
}

func readRest(path string, tail func() io.Reader, after string, afterAt int,
	rest func(container string, at int, line []byte, err error) error) error {
	segs, err := segmentFiles(filepath.Dir(path))
	if err != nil {
		return rest("", 0, nil, err)
	}
	type container struct {
		name string
		open func() (io.Reader, func(), error)
	}
	var all []container
	for _, seg := range segs {
		all = append(all, container{filepath.Base(seg.path), func() (io.Reader, func(), error) {
			f, err := os.Open(seg.path)
			if err != nil {
				return nil, nil, err
			}
			gz, err := gzip.NewReader(f)
			if err != nil {
				f.Close()
				return nil, nil, err
			}
			return gz, func() { gz.Close(); f.Close() }, nil
		}})
	}
	all = append(all, container{filepath.Base(path), func() (io.Reader, func(), error) {
		return tail(), func() {}, nil
	}})

	start, skip := 0, 0
	if after != "" {
		for i, c := range all {
			if c.name == after {
				start, skip = i, afterAt
				break
			}
		}
	}
	for i, c := range all[start:] {
		if i > 0 {
			skip = 0
		}
		r, done, err := c.open()
		if err != nil {
			if err := rest(c.name, 0, nil, err); err != nil {
				return err
			}
			continue
		}
		var stopped error
		reached := skip
		err = scanLines(r, func(line []byte, _, _ int64, at int) error {
			reached = at
			if at <= skip {
				return nil
			}
			if err := rest(c.name, at, line, nil); err != nil {
				stopped = err
				return err
			}
			return nil
		})
		done()
		if stopped != nil {
			return stopped
		}
		if err != nil {
			if err := rest(c.name, reached, nil, err); err != nil {
				return err
			}
		}
	}
	return nil
}
