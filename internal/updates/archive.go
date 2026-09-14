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
func readExactly(r io.Reader, name string) ([]byte, error) {
	return readBounded(r, maxDownloadSize, fmt.Sprintf("archive entry %q", name))
}

// .
// .
// .
// .
// .
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

// .
// .
// .
func extractFromTarGz(archiveBytes []byte) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}
		// .
		// .
		base := hdr.Name
		if idx := strings.LastIndexByte(hdr.Name, '/'); idx >= 0 {
			base = hdr.Name[idx+1:]
		}
		if base == "aii" || base == "aii-os" {
			return readExactly(tr, hdr.Name)
		}
	}
	return nil, fmt.Errorf("binary 'aii' not found in archive")
}

// .
// .
func extractFromZip(archiveBytes []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("zip reader: %w", err)
	}
	for _, f := range zr.File {
		base := f.Name
		if idx := strings.LastIndexByte(f.Name, '/'); idx >= 0 {
			base = f.Name[idx+1:]
		}
		if base == "aii.exe" || base == "aii-os.exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
			}
			defer rc.Close()
			return readExactly(rc, f.Name)
		}
	}
	return nil, fmt.Errorf("binary 'aii.exe' not found in archive")
}
