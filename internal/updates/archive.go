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

// extractFromTarGz extracts the `aii` binary from a .tar.gz archive.
// The archive is expected to contain a single `aii` entry (the release
// workflow archives only the binary, per the Commit 1 amendment).
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
		// Match "aii" or "aii-os" at the root of the archive (the
		// release workflow names the binary inside the archive).
		base := hdr.Name
		if idx := strings.LastIndexByte(hdr.Name, '/'); idx >= 0 {
			base = hdr.Name[idx+1:]
		}
		if base == "aii" || base == "aii-os" {
			return io.ReadAll(io.LimitReader(tr, maxDownloadSize))
		}
	}
	return nil, fmt.Errorf("binary 'aii' not found in archive")
}

// extractFromZip extracts the `aii.exe` binary from a .zip archive
// (Windows release).
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
			return io.ReadAll(io.LimitReader(rc, maxDownloadSize))
		}
	}
	return nil, fmt.Errorf("binary 'aii.exe' not found in archive")
}
