package witness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

type FileGuard interface {
	WitnessFiles(fn func() error) error
}

type Unguarded struct{}

func (Unguarded) WitnessFiles(fn func() error) error { return fn() }

var capturedFiles = []string{PendingRequestFileName, PendingReceiptFileName, EnvelopeFileName, OperationFileName}

func CapturedFileNames() []string { return append([]string(nil), capturedFiles...) }

func CaptureFiles(srcDir, dstDir string) ([]string, error) {
	var copied []string
	for _, name := range capturedFiles {
		raw, err := atomicfile.ReadFile(filepath.Join(srcDir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("capture %s: %w", name, err)
		}
		if name == OperationFileName {
			if err := CheckOperationSize(int64(len(raw))); err != nil {
				return nil, fmt.Errorf("capture: %w", err)
			}
		}
		published, err := atomicfile.WriteReplace(filepath.Join(dstDir, name), raw, 0o600)
		if err != nil {
			if published {
				return nil, fmt.Errorf("capture %s: published but not durable: %w", name, err)
			}
			return nil, fmt.Errorf("capture %s: %w", name, err)
		}
		copied = append(copied, name)
	}
	archives, err := os.ReadDir(filepath.Join(srcDir, AbandonedDirName))
	if errors.Is(err, os.ErrNotExist) {
		return copied, nil
	}
	if err != nil {
		return nil, fmt.Errorf("capture %s: %w", AbandonedDirName, err)
	}
	for _, e := range archives {
		if e.IsDir() || !ArchiveNameValid(e.Name()) {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dstDir, AbandonedDirName), 0o700); err != nil {
			return nil, fmt.Errorf("capture %s: %w", AbandonedDirName, err)
		}
		rel := AbandonedDirName + "/" + e.Name()
		from, to := filepath.Join(srcDir, AbandonedDirName, e.Name()), filepath.Join(dstDir, AbandonedDirName, e.Name())
		if err := os.Link(from, to); err != nil {
			raw, rerr := os.ReadFile(from)
			if rerr != nil {
				return nil, fmt.Errorf("capture %s: %w", rel, rerr)
			}
			if published, werr := atomicfile.WriteReplace(to, raw, 0o600); werr != nil {
				if published {
					return nil, fmt.Errorf("capture %s: published but not durable: %w", rel, werr)
				}
				return nil, fmt.Errorf("capture %s: %w", rel, werr)
			}
		}
		copied = append(copied, rel)
	}
	return copied, nil
}
