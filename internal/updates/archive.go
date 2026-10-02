package updates

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
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

func executable(name, want string) bool {
	name = strings.TrimRight(name, `/\`)
	return name[strings.LastIndexAny(name, `/\`)+1:] == want
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

func extractFromTarGz(archiveBytes []byte) (binary, helper []byte, err error) {
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
		want, into := tarBinary, &binary
		switch {
		case executable(hdr.Name, tarBinary):
		case executable(hdr.Name, vulkancap.Helper):
			want, into = vulkancap.Helper, &helper
		default:
			continue
		}
		e := entry{hdr.Name, hdr.Typeflag == tar.TypeReg, fmt.Sprintf("tar type %q", hdr.Typeflag), hdr.Size}
		if err := admit(e, want, *into != nil); err != nil {
			return nil, nil, err
		}
		if *into, err = readExactly(tr, hdr.Name); err != nil {
			return nil, nil, err
		}
	}
	if binary == nil {
		return nil, nil, fmt.Errorf("binary %q not found in archive", tarBinary)
	}
	return binary, helper, nil
}

func extractFromZip(archiveBytes []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("zip reader: %w", err)
	}
	var binary *zip.File
	for _, f := range zr.File {
		if !executable(f.Name, zipBinary) {
			continue
		}
		size := int64(min(f.UncompressedSize64, maxDownloadSize+1))
		e := entry{f.Name, f.Mode().IsRegular(), fmt.Sprintf("mode %v", f.Mode()), size}
		if err := admit(e, zipBinary, binary != nil); err != nil {
			return nil, err
		}
		binary = f
	}
	if binary == nil {
		return nil, fmt.Errorf("binary %q not found in archive", zipBinary)
	}
	rc, err := binary.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", binary.Name, err)
	}
	defer rc.Close()
	return readExactly(rc, binary.Name)
}
