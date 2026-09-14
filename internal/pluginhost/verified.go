package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
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
type VerifiedImage interface {
	// .
	// .
	Argv() []string
	// .
	// .
	ExtraFiles() []*os.File
	// .
	// .
	Binding() string
	// .
	// .
	// .
	// .
	handle() *os.File
	Close() error
}

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
// .
// .
// .
// .
// .
func OpenVerified(path, wantDigest string) (VerifiedImage, error) {
	img, err := bindImage(path)
	if err != nil {
		return nil, err
	}
	got, err := digestOfHandle(img.handle())
	if err != nil {
		img.Close()
		return nil, fmt.Errorf("read artifact: %w", err)
	}
	if got != wantDigest {
		img.Close()
		return nil, fmt.Errorf("artifact digest mismatch: read %s, verified %s", got, wantDigest)
	}
	return img, nil
}

// .
// .
// .
func digestOfHandle(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
