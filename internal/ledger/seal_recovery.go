package ledger

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

func (l *Ledger) RecoverSeal(pubKey []byte, heads HeadVerifier) (retErr error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.frozenReason != "" {
		return fmt.Errorf("seal recovery refused — %w (SAFE): %s", ErrFrozen, l.frozenReason)
	}
	if l.dirLock == nil {
		return os.ErrClosed
	}
	if l.sealErr == nil {
		return nil
	}
	tail, err := os.Open(l.path)
	if err != nil {
		return err
	}
	defer func() {
		if tail != nil {
			retErr = errors.Join(retErr, tail.Close())
		}
	}()
	info, err := tail.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return &os.PathError{Op: "recover regular ledger", Path: l.path, Err: os.ErrInvalid}
	}
	end, err := completeTailEnd(tail, info.Size())
	if err != nil {
		return err
	}
	var boundary Boundary
	if _, err := verifyChain(func(fn func(*Event) error) error {
		return stream(l.path, io.NewSectionReader(tail, 0, end), fn)
	}, pubKey, heads, func(evt *Event) error {
		boundary = Boundary{Seq: evt.Seq, Hash: evt.EntryHash()}
		return nil
	}); err != nil {
		var pathErr *os.PathError
		var systemErr syscall.Errno
		if errors.As(err, &pathErr) || errors.As(err, &systemErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSealedWithoutWitness) || errors.Is(err, ErrWitnessKeyUnknown) {
			return fmt.Errorf("seal recovery could not verify the containers: %w", err)
		}
		return l.refuseSealIntegrity(err)
	}
	if boundary.Seq != l.lastSeq || boundary.Hash != l.lastHash {
		return l.refuseSealIntegrity(fmt.Errorf("verified boundary (%d,%s) differs from the acknowledged boundary (%d,%s)", boundary.Seq, boundary.Hash, l.lastSeq, l.lastHash))
	}

	if err := atomicfile.SyncDir(l.dir); err != nil {
		return fmt.Errorf("seal recovery sync: %w", err)
	}
	if end < info.Size() {
		if err := trimPreservedSuffix(l.path, tail, end, info.Size(), nil); err != nil {
			return err
		}
	}
	err = tail.Close()
	tail = nil
	if err != nil {
		return err
	}
	segs, err := listSegments(l.dir)
	if err != nil {
		return err
	}
	var sealed Boundary
	if len(segs) > 0 {
		sealed.Seq, sealed.Hash, err = segmentEnd(&segs[len(segs)-1])
		if err != nil {
			return err
		}
	}
	if l.file != nil {
		if err := l.file.Sync(); err != nil {
			return err
		}
		closing := l.file
		l.file, l.writer = nil, nil
		if err := closing.Close(); err != nil {
			return err
		}
	}
	if err := sweepDebris(l.dir); err != nil {
		return err
	}
	if len(segs) > 0 {
		if err := reconcileTail(l.path, &segs[len(segs)-1]); err != nil {
			return err
		}
	}
	f, err := atomicfile.OpenAppendRemovable(l.path, 0600)
	if err != nil {
		return err
	}
	if err := lockLedgerFile(f); err != nil {
		return errors.Join(err, f.Close())
	}
	l.file, l.writer = f, bufio.NewWriter(f)
	l.segments, l.sealedSeq, l.sealedHash = segs, sealed.Seq, sealed.Hash
	l.sealErr = nil
	return nil
}

func (l *Ledger) refuseSealIntegrity(cause error) error {
	err := fmt.Errorf("%w: seal recovery: %w", ErrTailIntegrity, cause)
	l.frozenReason = err.Error()
	return err
}
