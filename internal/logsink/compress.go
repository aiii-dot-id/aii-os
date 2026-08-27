package logsink

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CompressOlder gzips rotated (uncompressed) logs older than
// CompressDays, then prunes. Returns (compressed, removed, err).
func (s *Sink) CompressOlder() (int, int, error) {
	days := s.cfg.compressDays()
	compressed := 0
	if days > 0 {
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		names, err := s.rotated()
		if err != nil {
			return 0, 0, err
		}
		for _, n := range names {
			if strings.HasSuffix(n, gzipExt) {
				continue
			}
			src := filepath.Join(s.dir, n)
			fi, err := os.Stat(src)
			if err != nil {
				continue
			}
			if fi.ModTime().Before(cutoff) {
				if err := gzipFile(src, src+gzipExt); err != nil {
					return compressed, 0, err
				}
				compressed++
			}
		}
	}
	removed, err := s.Prune()
	return compressed, removed, err
}

// gzipFile streams src into dst.gz and removes src only after the
// write succeeds — a failed compression must not destroy the original.
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := zw.Close(); err != nil { // flushes the gzip trailer
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(src)
}
