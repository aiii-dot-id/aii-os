package ledger

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

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
// .

const (
	segmentPrefix     = "segment-"
	segmentSuffix     = ".jsonl.gz"
	segmentTempPrefix = ".segment-"
	tailTempPrefix    = ".ledger.tail-"
	tempSuffix        = ".tmp"
)

var (
	// .
	// .
	ErrSegmentWithoutHead = errors.New("segment does not close under a witness head")
	// .
	// .
	// .
	ErrSealedWithoutWitness = errors.New("sealed segment present but no witness key to verify its head")
	// .
	// .
	ErrTailConflict = errors.New("tail conflicts with a sealed segment")
	// .
	// .
	// .
	// .
	ErrRecordUnreadable = errors.New("the record cannot be read")
	// .
	// .
	ErrSegmentSet = errors.New("segment set is not one contiguous run from record 1")
	// .
	// .
	// .
	// .
	ErrWitnessKeyUnknown = errors.New("no persisted witness key for this head")
)

// .
// .
// .
// .
// .
type HeadVerifier interface {
	VerifyHead(evt *Event) error
}

// .
// .
func (e *Event) Sealed() bool { return e.sealed }

// .
// .
// .
// .
var sealStep = func(step string) {}

type segment struct {
	first, last uint64
	path        string
}

func segmentName(first, last uint64) string {
	return segmentPrefix + strconv.FormatUint(first, 10) + "-" + strconv.FormatUint(last, 10) + segmentSuffix
}

// .
func IsSegmentName(name string) bool {
	_, _, ok := parseSegmentName(name)
	return ok
}

func parseSegmentName(name string) (first, last uint64, ok bool) {
	if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, segmentSuffix) {
		return 0, 0, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
	a, b, found := strings.Cut(body, "-")
	if !found {
		return 0, 0, false
	}
	first, err := strconv.ParseUint(a, 10, 64)
	if err != nil || strconv.FormatUint(first, 10) != a {
		return 0, 0, false
	}
	last, err = strconv.ParseUint(b, 10, 64)
	if err != nil || strconv.FormatUint(last, 10) != b {
		return 0, 0, false
	}
	if first == 0 || last < first {
		return 0, 0, false
	}
	return first, last, true
}

// .
// .
// .
func listSegments(dir string) ([]segment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var segs []segment
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		first, last, ok := parseSegmentName(e.Name())
		if !ok {
			continue
		}
		segs = append(segs, segment{first: first, last: last, path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].first < segs[j].first })
	var next uint64 = 1
	for _, s := range segs {
		if s.first != next {
			return nil, fmt.Errorf("%w: %s begins at %d, expected %d", ErrSegmentSet, filepath.Base(s.path), s.first, next)
		}
		next = s.last + 1
	}
	return segs, nil
}

// .
// .
// .
func sweepDebris(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, tempSuffix) {
			continue
		}
		if strings.HasPrefix(n, segmentTempPrefix) || strings.HasPrefix(n, tailTempPrefix) {
			if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// .
// .
// .
// .
func scanLines(r io.Reader, fn func(line []byte, offset, size int64) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), MaxEventLineBytes)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i+1], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var offset int64
	for sc.Scan() {
		tok := sc.Bytes()
		size := int64(len(tok))
		line := bytes.TrimSpace(tok)
		if len(line) > 0 {
			if err := fn(line, offset, size); err != nil {
				return err
			}
		}
		offset += size
	}
	return sc.Err()
}

// .
// .
// .
// .
func streamSegment(seg *segment, fn func(*Event) error) error {
	f, err := os.Open(seg.path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(seg.path), err)
	}
	defer gz.Close()
	var last *Event
	n := 0
	err = scanLines(gz, func(line []byte, _, _ int64) error {
		evt, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("%s record %d: malformed line: %w", filepath.Base(seg.path), n+1, err)
		}
		evt.sealed = true
		if n == 0 && evt.Seq != seg.first {
			return fmt.Errorf("%w: %s begins with record %d", ErrSegmentSet, filepath.Base(seg.path), evt.Seq)
		}
		n++
		last = &evt
		return fn(&evt)
	})
	if err != nil {
		return err
	}
	if last == nil {
		return fmt.Errorf("%w: %s is empty", ErrSegmentWithoutHead, filepath.Base(seg.path))
	}
	if last.Seq != seg.last {
		return fmt.Errorf("%w: %s ends with record %d", ErrSegmentSet, filepath.Base(seg.path), last.Seq)
	}
	if last.Type != EventSystemWitnessed || last.Sig == "" {
		return fmt.Errorf("%w: %s ends with %s (record %d)", ErrSegmentWithoutHead, filepath.Base(seg.path), last.Type, last.Seq)
	}
	return nil
}

// .
// .
// .
type overlapChecker struct {
	gz      *gzip.Reader
	f       *os.File
	sc      *bufio.Scanner
	seg     *segment
	next    uint64
	pending *Event
}

func newOverlapChecker(seg *segment, from uint64) (*overlapChecker, error) {
	f, err := os.Open(seg.path)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", filepath.Base(seg.path), err)
	}
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 64*1024), MaxEventLineBytes)
	oc := &overlapChecker{gz: gz, f: f, sc: sc, seg: seg, next: from}
	// .
	for {
		evt, ok, err := oc.read()
		if err != nil {
			oc.close()
			return nil, err
		}
		if !ok {
			oc.close()
			return nil, fmt.Errorf("%w: %s ends before record %d", ErrSegmentSet, filepath.Base(seg.path), from)
		}
		if evt.Seq+1 == from {
			return oc, nil
		}
		if evt.Seq >= from {
			// .
			// .
			if evt.Seq == from {
				oc.pending = &evt
				return oc, nil
			}
			oc.close()
			return nil, fmt.Errorf("%w: %s skips record %d", ErrSegmentSet, filepath.Base(seg.path), from)
		}
	}
}

func (oc *overlapChecker) read() (Event, bool, error) {
	if oc.pending != nil {
		e := *oc.pending
		oc.pending = nil
		return e, true, nil
	}
	for oc.sc.Scan() {
		line := bytes.TrimSpace(oc.sc.Bytes())
		if len(line) == 0 {
			continue
		}
		evt, err := decodeEvent(line)
		if err != nil {
			return Event{}, false, fmt.Errorf("%s: malformed line: %w", filepath.Base(oc.seg.path), err)
		}
		return evt, true, nil
	}
	return Event{}, false, oc.sc.Err()
}

// .
// .
func (oc *overlapChecker) check(tail *Event) (finished bool, err error) {
	if tail.Seq != oc.next {
		return false, fmt.Errorf("%w: tail record %d where %d was expected inside the overlap", ErrTailConflict, tail.Seq, oc.next)
	}
	sealed, ok, err := oc.read()
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("%w: %s ends before record %d", ErrSegmentSet, filepath.Base(oc.seg.path), tail.Seq)
	}
	if sealed.Seq != tail.Seq || sealed.EntryHash() != tail.EntryHash() {
		return false, fmt.Errorf("%w: record %d differs between the tail and %s", ErrTailConflict, tail.Seq, filepath.Base(oc.seg.path))
	}
	oc.next++
	return tail.Seq == oc.seg.last, nil
}

func (oc *overlapChecker) close() {
	oc.gz.Close()
	oc.f.Close()
}

// .
// .
// .
func streamTail(f *os.File, newest *segment, fn func(*Event) error) error {
	var oc *overlapChecker
	defer func() {
		if oc != nil {
			oc.close()
		}
	}()
	n := 0
	return scanLines(f, func(line []byte, _, _ int64) error {
		evt, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("record %d: malformed line: %w", n+1, err)
		}
		n++
		if newest != nil && evt.Seq <= newest.last {
			if oc == nil {
				if n != 1 || evt.Seq < newest.first {
					return fmt.Errorf("%w: tail record %d repeats a sealed record outside an overlap", ErrTailConflict, evt.Seq)
				}
				oc, err = newOverlapChecker(newest, evt.Seq)
				if err != nil {
					return err
				}
			}
			finished, err := oc.check(&evt)
			if err != nil {
				return err
			}
			if finished {
				oc.close()
				oc = nil
				newest = nil
			}
			return nil
		}
		if oc != nil {
			return fmt.Errorf("%w: tail leaves the overlap at record %d before the segment ends", ErrTailConflict, evt.Seq)
		}
		return fn(&evt)
	})
}

// .
// .
func segmentEnd(seg *segment) (uint64, string, error) {
	var seq uint64
	var hash string
	if err := streamSegment(seg, func(evt *Event) error {
		seq, hash = evt.Seq, evt.EntryHash()
		return nil
	}); err != nil {
		return 0, "", err
	}
	return seq, hash, nil
}

// .
// .
// .
// .
func reconcileTail(path string, newest *segment) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var first *Event
	err = scanLines(f, func(line []byte, _, _ int64) error {
		evt, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("tail record 1: malformed line: %w", err)
		}
		first = &evt
		return ErrStop
	})
	if err != nil && !errors.Is(err, ErrStop) {
		f.Close()
		return err
	}
	if first == nil || first.Seq > newest.last {
		return f.Close()
	}
	if first.Seq < newest.first {
		f.Close()
		return fmt.Errorf("%w: tail begins at record %d, before the newest segment (%d-%d)", ErrTailConflict, first.Seq, newest.first, newest.last)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	oc, err := newOverlapChecker(newest, first.Seq)
	if err != nil {
		f.Close()
		return err
	}
	defer oc.close()
	var keepFrom int64 = -1
	err = scanLines(f, func(line []byte, offset, size int64) error {
		evt, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("tail: malformed line: %w", err)
		}
		finished, err := oc.check(&evt)
		if err != nil {
			return err
		}
		if finished {
			keepFrom = offset + size
			return ErrStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, ErrStop) {
		f.Close()
		return err
	}
	if keepFrom < 0 {
		// .
		// .
		keepFrom, err = f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return err
		}
	}
	return replaceTail(path, f, keepFrom)
}

// .
// .
// .
func replaceTail(path string, src *os.File, keepFrom int64) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, tailTempPrefix+strconv.FormatInt(time.Now().UnixNano(), 10)+tempSuffix)
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		src.Close()
		return err
	}
	if _, err := src.Seek(keepFrom, io.SeekStart); err != nil {
		out.Close()
		os.Remove(tmp)
		src.Close()
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		src.Close()
		return err
	}
	if err := src.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	sealStep("tail-written")
	if err := atomicfile.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	sealStep("tail-renamed")
	if err := atomicfile.SyncDir(dir); err != nil {
		return err
	}
	sealStep("tail-published")
	return nil
}

// .
func (l *Ledger) SealedSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sealedSeq
}

// .
// .
// .
// .
// .
// .
func (l *Ledger) Seal(head uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.frozenReason != "" {
		return fmt.Errorf("seal refused — ledger frozen (SAFE): %s", l.frozenReason)
	}
	if head > l.lastSeq {
		return fmt.Errorf("seal: record %d is beyond the last record %d", head, l.lastSeq)
	}
	if head <= l.sealedSeq {
		return fmt.Errorf("seal: record %d is already sealed (sealed through %d)", head, l.sealedSeq)
	}
	first := l.sealedSeq + 1
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			return fmt.Errorf("seal: flush: %w", err)
		}
	}
	tmp := filepath.Join(l.dir, segmentTempPrefix+strconv.FormatUint(first, 10)+"-"+strconv.FormatUint(head, 10)+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)+tempSuffix)
	afterHead, headHash, err := l.writeSegment(tmp, first, head)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("seal: %w", err)
	}
	sealStep("segment-written")
	final := filepath.Join(l.dir, segmentName(first, head))
	if err := atomicfile.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("seal: publish segment: %w", err)
	}
	sealStep("segment-renamed")
	if err := atomicfile.SyncDir(l.dir); err != nil {
		return fmt.Errorf("seal: sync directory: %w", err)
	}
	sealStep("segment-published")
	// .
	// .
	// .
	if err := l.replaceTailFrom(afterHead); err != nil {
		return fmt.Errorf("seal: %w", err)
	}
	l.sealedSeq, l.sealedHash = head, headHash
	l.segments = append(l.segments, segment{first: first, last: head, path: final})
	return nil
}

// .
// .
// .
// .
// .
func (l *Ledger) writeSegment(tmp string, first, head uint64) (int64, string, error) {
	src, err := os.Open(l.path)
	if err != nil {
		return 0, "", err
	}
	defer src.Close()
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	gz, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		out.Close()
		return 0, "", err
	}
	var afterHead int64 = -1
	var headHash string
	var expect = first
	err = scanLines(src, func(line []byte, offset, size int64) error {
		evt, err := decodeEvent(line)
		if err != nil {
			return fmt.Errorf("tail: malformed line: %w", err)
		}
		if evt.Seq < first {
			return nil
		}
		if evt.Seq != expect {
			return fmt.Errorf("tail record %d where %d was expected", evt.Seq, expect)
		}
		expect++
		if evt.Seq < head {
			if evt.Sig == "" {
				return fmt.Errorf("%w: tail record %d carries no proof", ErrUnsignedRecord, evt.Seq)
			}
			evt.Sig = ""
			b, err := evt.MarshalJSON()
			if err != nil {
				return err
			}
			if _, err := gz.Write(append(b, '\n')); err != nil {
				return err
			}
			return nil
		}
		if evt.Type != EventSystemWitnessed || evt.Sig == "" {
			return fmt.Errorf("%w: record %d is %s", ErrSegmentWithoutHead, evt.Seq, evt.Type)
		}
		if _, err := gz.Write(append(bytes.Clone(line), '\n')); err != nil {
			return err
		}
		afterHead = offset + size
		headHash = evt.EntryHash()
		return ErrStop
	})
	if err != nil && !errors.Is(err, ErrStop) {
		gz.Close()
		out.Close()
		return 0, "", err
	}
	if afterHead < 0 {
		gz.Close()
		out.Close()
		return 0, "", fmt.Errorf("tail ends before record %d", head)
	}
	if err := gz.Close(); err != nil {
		out.Close()
		return 0, "", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return 0, "", err
	}
	if err := out.Close(); err != nil {
		return 0, "", err
	}
	return afterHead, headHash, nil
}

// .
// .
// .
// .
// .
func (l *Ledger) replaceTailFrom(keepFrom int64) error {
	src, err := os.Open(l.path)
	if err != nil {
		return err
	}
	if l.file != nil {
		if err := l.file.Sync(); err != nil {
			src.Close()
			return fmt.Errorf("sync before rewrite: %w", err)
		}
		if err := l.file.Close(); err != nil {
			src.Close()
			return fmt.Errorf("close before rewrite: %w", err)
		}
		l.file, l.writer = nil, nil
	}
	if err := replaceTail(l.path, src, keepFrom); err != nil {
		l.frozenReason = "sealing could not rewrite the tail: " + err.Error()
		return err
	}
	f, err := atomicfile.OpenAppendRemovable(l.path, 0600)
	if err != nil {
		l.frozenReason = "sealing could not reopen the ledger: " + err.Error()
		return fmt.Errorf("reopen after rewrite: %w", err)
	}
	if err := lockLedgerFile(f); err != nil {
		f.Close()
		l.frozenReason = "sealing could not relock the ledger: " + err.Error()
		return fmt.Errorf("relock after rewrite: %w", err)
	}
	l.file = f
	l.writer = bufio.NewWriter(f)
	return nil
}
