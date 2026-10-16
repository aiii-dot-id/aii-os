package updates

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

func readExactly(r io.Reader, name string) ([]byte, error) {
	return readBounded(r, maxDownloadSize, fmt.Sprintf("archive entry %q", name))
}

func readBounded(r io.Reader, limit int64, what string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s exceeds the %d byte limit — refusing rather than installing a truncated executable", what, limit)
	}
	return b, nil
}

const (
	tarBinary = "aii"
	zipBinary = "aii.exe"
)

type entry struct {
	name    string
	regular bool
	kind    string
	size    int64
}

type installedFile struct {
	name string
	data []byte
}

func named(name string, want []string) (string, bool) {
	name = strings.TrimRight(name, `/\`)
	base := name[strings.LastIndexAny(name, `/\`)+1:]
	for _, w := range want {
		if base == w {
			return w, true
		}
	}
	return "", false
}

func admit(e entry, want string, seen bool) error {
	var why string
	switch {
	case seen:
		why = "appears twice"
	case !e.regular:
		why = fmt.Sprintf("is not a regular file (%s)", e.kind)
	case e.name != want:
		why = "is not at the archive root"
	case e.size <= 0:
		why = "is empty"
	case e.size > maxDownloadSize:
		why = fmt.Sprintf("exceeds the %d byte limit", maxDownloadSize)
	default:
		return nil
	}
	return fmt.Errorf("archive entry %q: %s %s — refusing the release", e.name, want, why)
}

type taken struct {
	binary string
	beside []string
	got    map[string][]byte
}

func newTaken(platform string) (*taken, error) {
	programs := ShippedPrograms(platform)
	if len(programs) == 0 {
		return nil, fmt.Errorf("no release archive is defined for platform %q", platform)
	}
	return &taken{binary: programs[0].Name, beside: installedBeside(platform), got: map[string][]byte{}}, nil
}

func (t *taken) wants(name string) (string, bool) {
	if w, ok := named(name, []string{t.binary}); ok {
		return w, true
	}
	return named(name, t.beside)
}

func (t *taken) result() ([]byte, []installedFile, error) {
	binary, ok := t.got[t.binary]
	if !ok {
		return nil, nil, fmt.Errorf("binary %q not found in archive", t.binary)
	}
	var beside []installedFile
	for _, name := range t.beside {
		if data, ok := t.got[name]; ok {
			beside = append(beside, installedFile{name, data})
		}
	}
	return binary, beside, nil
}

func extractFromTarGz(platform string, archiveBytes []byte) (binary []byte, beside []installedFile, err error) {
	t, err := newTaken(platform)
	if err != nil {
		return nil, nil, err
	}
	gzr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("tar read: %w", err)
		}
		want, ok := t.wants(hdr.Name)
		if !ok {
			continue
		}
		_, seen := t.got[want]
		e := entry{hdr.Name, hdr.Typeflag == tar.TypeReg, fmt.Sprintf("tar type %q", hdr.Typeflag), hdr.Size}
		if err := admit(e, want, seen); err != nil {
			return nil, nil, err
		}
		if t.got[want], err = readExactly(tr, hdr.Name); err != nil {
			return nil, nil, err
		}
	}
	return t.result()
}

func extractFromZip(platform string, archiveBytes []byte) (binary []byte, beside []installedFile, err error) {
	t, err := newTaken(platform)
	if err != nil {
		return nil, nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, nil, fmt.Errorf("zip reader: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		want, ok := t.wants(f.Name)
		if !ok {
			continue
		}
		size := int64(min(f.UncompressedSize64, maxDownloadSize+1))
		e := entry{f.Name, f.Mode().IsRegular(), fmt.Sprintf("mode %v", f.Mode()), size}
		if err := admit(e, want, files[want] != nil); err != nil {
			return nil, nil, err
		}
		files[want] = f
	}
	for want, f := range files {
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		t.got[want], err = readExactly(rc, f.Name)
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return t.result()
}
