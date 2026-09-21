package escrow

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"filippo.io/age"
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
const SnapshotSuffix = ".tar.age"

// .
// .
// .
func NewSnapshotKey() ([]byte, error) {
	id, err := age.GenerateHybridIdentity()
	if err != nil {
		return nil, fmt.Errorf("snapshot key: %w", err)
	}
	return []byte("# AII snapshot key — decrypts this identity's snapshots (*.tar.age). Keep it; escrow it (aii escrow).\n" +
		"# recipient: " + id.Recipient().String() + "\n" + id.String() + "\n"), nil
}

// .
// .
// .
// .
func EncryptSnapshot(dir string, names []string, recipient, dst string) (retErr error) {
	r, err := age.ParseHybridRecipient(recipient)
	if err != nil {
		return fmt.Errorf("encrypt snapshot: recipient: %w", err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("encrypt snapshot: %w", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("encrypt snapshot: %w", cerr)
		}
		if retErr != nil {
			os.Remove(dst)
		}
	}()
	buffered := bufio.NewWriterSize(out, 1<<20)
	enc, err := age.Encrypt(buffered, r)
	if err != nil {
		return fmt.Errorf("encrypt snapshot: %w", err)
	}
	tw := tar.NewWriter(enc)
	for _, name := range names {
		if err := addToTar(tw, dir, name); err != nil {
			return fmt.Errorf("encrypt snapshot: %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("encrypt snapshot: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encrypt snapshot: %w", err)
	}
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("encrypt snapshot: %w", err)
	}
	return out.Sync()
}

func addToTar(tw *tar.Writer, dir, name string) error {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a plain file")
	}
	// .
	// .
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	n, err := io.Copy(tw, f)
	if err != nil {
		return err
	}
	if n != info.Size() {
		return fmt.Errorf("changed size while it was read (%d of %d bytes)", n, info.Size())
	}
	return nil
}

// .
// .
var ErrSnapshotMismatch = errors.New("the encrypted snapshot is not the set that was proved")

// .
// .
// .
// .
// .
// .
func VerifySnapshot(src string, snapshotKey []byte, names []string, want map[string]string) error {
	return walkSnapshot(src, snapshotKey, func(i int, h *tar.Header, r io.Reader) error {
		if i >= len(names) || h.Name != names[i] {
			return fmt.Errorf("%w: member %d is %q", ErrSnapshotMismatch, i, h.Name)
		}
		sum := sha256.New()
		if _, err := io.Copy(sum, r); err != nil {
			return err
		}
		if got := hex.EncodeToString(sum.Sum(nil)); got != want[h.Name] {
			return fmt.Errorf("%w: %s hashes to %s, proved as %s", ErrSnapshotMismatch, h.Name, got, want[h.Name])
		}
		return nil
	}, func(n int) error {
		if n != len(names) {
			return fmt.Errorf("%w: it holds %d files, %d were proved", ErrSnapshotMismatch, n, len(names))
		}
		return nil
	})
}

// .
// .
// .
// .
func OpenSnapshot(src string, snapshotKey []byte, outDir string) (names []string, retErr error) {
	if _, err := os.Stat(outDir); err == nil {
		return nil, fmt.Errorf("open snapshot: %s already exists — a snapshot is never unpacked over anything", outDir)
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	defer func() {
		if retErr != nil {
			os.RemoveAll(outDir)
		}
	}()
	err := walkSnapshot(src, snapshotKey, func(_ int, h *tar.Header, r io.Reader) error {
		clean := path.Clean(h.Name)
		if clean != h.Name || path.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(clean, '\\') {
			return fmt.Errorf("member %q is not a plain path inside the snapshot", h.Name)
		}
		dst := filepath.Join(outDir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		names = append(names, clean)
		return f.Close()
	}, func(int) error { return nil })
	if err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	return names, nil
}

// .
// .
func walkSnapshot(src string, snapshotKey []byte, fn func(i int, h *tar.Header, r io.Reader) error, done func(n int) error) error {
	ids, err := age.ParseIdentities(bytes.NewReader(snapshotKey))
	if err != nil {
		return fmt.Errorf("snapshot key: %w", err)
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	plain, err := age.Decrypt(bufio.NewReaderSize(f, 1<<20), ids...)
	if err != nil {
		return fmt.Errorf("it does not decrypt under this snapshot key: %w", err)
	}
	tr := tar.NewReader(plain)
	n := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("it decrypts, and what it holds is not an archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("member %q is not a plain file", h.Name)
		}
		if err := fn(n, h, tr); err != nil {
			return err
		}
		n++
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if err := onlyPaddingFollows(plain); err != nil {
		return err
	}
	return done(n)
}

// .
// .
// .
// .
// .
// .
// .
// .
func onlyPaddingFollows(r io.Reader) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		for _, b := range buf[:n] {
			if b != 0 {
				return fmt.Errorf("the archive ends and the file does not — something follows its end-of-archive marker")
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the archive is cut short: %w", err)
		}
	}
}
