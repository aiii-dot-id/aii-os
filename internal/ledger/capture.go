package ledger

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// .
// .
// .
// .
type Capture struct {
	LastSeq   uint64
	LastHash  string
	SealedSeq uint64
	// .
	// .
	// .
	// .
	Segments []string
}

// .
// .
var ErrCaptureRefused = errors.New("ledger capture refused")

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
// .
func (l *Ledger) Capture(tailDst string) (_ Capture, retErr error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return Capture{}, fmt.Errorf("%w: the ledger is not open", ErrCaptureRefused)
	}
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			return Capture{}, fmt.Errorf("capture: flush: %w", err)
		}
	}
	out, err := os.OpenFile(tailDst, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Capture{}, fmt.Errorf("capture: %w", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("capture: close the tail copy: %w", cerr)
		}
		if retErr != nil {
			os.Remove(tailDst)
		}
	}()
	src, err := os.Open(l.path)
	if err != nil {
		return Capture{}, fmt.Errorf("capture: open the tail: %w", err)
	}
	_, err = io.Copy(out, src)
	if cerr := src.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Capture{}, fmt.Errorf("capture: copy the tail: %w", err)
	}
	if err := out.Sync(); err != nil {
		return Capture{}, fmt.Errorf("capture: sync the tail copy: %w", err)
	}

	last, err := readTailEvents(out, 0)
	if err != nil {
		return Capture{}, fmt.Errorf("%w: the tail does not read as records: %w", ErrCaptureRefused, err)
	}
	switch {
	case len(last) == 0:
		if l.lastSeq != l.sealedSeq || l.lastHash != l.sealedHash {
			return Capture{}, fmt.Errorf("%w: the tail is empty but the record runs to %d (sealed through %d)",
				ErrCaptureRefused, l.lastSeq, l.sealedSeq)
		}
	// .
	// .
	// .
	case last[len(last)-1].EntryHash() != l.lastHash:
		return Capture{}, fmt.Errorf("%w: the tail ends at record %d, which is not record %d as the ledger appended it",
			ErrCaptureRefused, last[len(last)-1].Seq, l.lastSeq)
	}

	c := Capture{LastSeq: l.lastSeq, LastHash: l.lastHash, SealedSeq: l.sealedSeq}
	for _, s := range l.segments {
		c.Segments = append(c.Segments, s.path)
	}
	return c, nil
}
