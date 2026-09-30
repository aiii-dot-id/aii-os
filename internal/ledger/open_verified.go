package ledger

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func OpenVerified(path string, pubKey []byte, heads HeadVerifier, visit func(*Event) error, check func(uint64) error) (*Ledger, int, error) {
	return openVerified(path, pubKey, heads, visit, check, nil)
}

func openVerified(path string, pubKey []byte, heads HeadVerifier, visit func(*Event) error, check func(uint64) error, afterPreserve func() error) (*Ledger, int, error) {
	var count int
	var boundary Boundary
	l, err := openLedger(path, func() (retErr error) {
		f, err := os.Open(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		var size int64
		var tail io.Reader = bytes.NewReader(nil)
		if f != nil {
			defer func() { retErr = errors.Join(retErr, f.Close()) }()
			info, err := f.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return &os.PathError{Op: "open regular ledger", Path: path, Err: os.ErrInvalid}
			}
			size = info.Size()
		}
		end, err := completeTailEnd(f, size)
		if err != nil {
			return err
		}
		if f != nil {
			tail = io.NewSectionReader(f, 0, end)
		}
		count, err = verifyChain(func(fn func(*Event) error) error {
			return stream(path, tail, fn)
		}, pubKey, heads, func(evt *Event) error {
			boundary = Boundary{Seq: evt.Seq, Hash: evt.EntryHash()}
			if visit != nil {
				return visit(evt)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("%w: existing identity has no complete ledger records", ErrRecordUnreadable)
		}
		if check != nil {
			if err := check(boundary.Seq); err != nil {
				return err
			}
		}
		if end < size {
			return trimPreservedSuffix(path, f, end, size, afterPreserve)
		}
		return nil
	})
	if err != nil {
		return nil, count, err
	}
	if l.LastSeq() != boundary.Seq || l.LastHash() != boundary.Hash {
		return nil, count, errors.Join(fmt.Errorf("%w: writable boundary differs from the verified prefix", ErrTailConflict), l.Close())
	}
	return l, count, nil
}

func trimPreservedSuffix(path string, f *os.File, end, size int64, afterPreserve func() error) error {
	if err := preserveSuffix(path, io.NewSectionReader(f, end, size-end)); err != nil {
		return err
	}
	if afterPreserve != nil {
		if err := afterPreserve(); err != nil {
			return err
		}
	}

	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	err = w.Truncate(end)
	if err == nil {
		err = w.Sync()
	}
	err = errors.Join(err, w.Close())
	if err == nil {
		logsink.Warn("ledger.error", "recovered an incomplete final frame (%d bytes); exact bytes preserved beside %s in its content-named torn sidecar", size-end, path)
	}
	return err
}

func completeTailEnd(f *os.File, size int64) (int64, error) {
	if size == 0 {
		return 0, nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], size-1); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		return size, nil
	}
	buf := make([]byte, 64*1024)
	end := int64(0)
	for pos := size; pos > 0; {
		n := min(pos, int64(len(buf)))
		pos -= n
		if _, err := f.ReadAt(buf[:n], pos); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			end = pos + int64(i) + 1
			break
		}
	}
	r := bufio.NewReader(io.NewSectionReader(f, end, size-end))
	for {
		ch, _, err := r.ReadRune()
		if err == io.EOF {
			return size, nil
		}
		if err != nil {
			return 0, err
		}
		if !unicode.IsSpace(ch) {
			return end, nil
		}
	}
}

func preserveSuffix(path string, suffix io.Reader) (retErr error) {
	f, err := os.CreateTemp(filepath.Dir(path), tailTempPrefix+"torn-*"+tempSuffix)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(f.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, err)
		}
	}()
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), suffix)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	published, err := atomicfile.Replace(f.Name(), fmt.Sprintf("%s.torn-%x", path, h.Sum(nil)))
	if err != nil {
		if published {
			return fmt.Errorf("torn suffix published but durability unconfirmed; original tail retained: %w", err)
		}
		return fmt.Errorf("torn suffix publication failed; original tail retained: %w", err)
	}
	return nil
}
