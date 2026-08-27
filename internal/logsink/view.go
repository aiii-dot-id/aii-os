package logsink

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File describes one log file for listing.
type File struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Modified   string `json:"modified"`
	Compressed bool   `json:"compressed"`
}

// List returns the live log followed by rotated logs, newest first.
func (s *Sink) List() ([]File, error) {
	var files []File
	if fi, err := os.Stat(filepath.Join(s.dir, LiveName)); err == nil {
		files = append(files, File{
			Name:     LiveName,
			Size:     fi.Size(),
			Modified: fi.ModTime().UTC().Format(time.RFC3339),
		})
	}
	names, err := s.rotated()
	if err != nil {
		return files, err
	}
	// newest first: reverse the oldest-first slice
	for i := len(names) - 1; i >= 0; i-- {
		n := names[i]
		fi, err := os.Stat(filepath.Join(s.dir, n))
		if err != nil {
			continue
		}
		files = append(files, File{
			Name:       n,
			Size:       fi.Size(),
			Modified:   fi.ModTime().UTC().Format(time.RFC3339),
			Compressed: strings.HasSuffix(n, gzipExt),
		})
	}
	return files, nil
}

// tailReadBytes caps how much of a log one Tail loads (D49, Sev
// 2026-08-26): a long-lived live log must not balloon one dashboard
// query into the identity's whole memory. 1 MiB carries thousands of
// lines — far past the 400-line view.
const tailReadBytes = 1 << 20

// maxGunzipBytes bounds the total decompressed stream a Tail will
// walk. Sink-rotated logs land far below it; a stream that keeps
// going is a decompression bomb and is refused with the route to the
// real bytes.
const maxGunzipBytes = 64 << 20

// Tail returns the last n lines of the named log (gzip-aware). Only
// names this sink owns are accepted; a viewer must never become a
// general file reader. Reads are BOUNDED: plain files are read from
// the end, gzip streams keep only a sliding tail — and when the cut
// eats into the requested window, the first line says so and names
// the route (R18: omission declared, never silent).
func (s *Sink) Tail(name string, n int) ([]string, error) {
	if filepath.Base(name) != name ||
		(!strings.HasPrefix(name, rotatedPrefix) && name != LiveName) ||
		(!strings.HasSuffix(name, rotatedExt) && !strings.HasSuffix(name, rotatedExt+gzipExt)) {
		return nil, fmt.Errorf("logsink: %q is not a log file this sink owns", name)
	}
	if n <= 0 {
		n = 400
	}
	var data []byte
	var truncated bool
	var err error
	path := filepath.Join(s.dir, name)
	if strings.HasSuffix(name, gzipExt) {
		data, truncated, err = gunzipTail(path, tailReadBytes)
	} else {
		data, truncated, err = plainTail(path, tailReadBytes)
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(data), "\n")
	if truncated {
		// Drop the partial first line the byte cut left behind.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	} else if truncated {
		lines = append([]string{fmt.Sprintf("… earlier lines not shown (the view reads the last %d bytes; download %s for the rest)", tailReadBytes, name)}, lines...)
	}
	return lines, nil
}

// plainTail reads at most limit bytes from the END of the file.
func plainTail(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if fi.Size() <= limit {
		b, rerr := io.ReadAll(f)
		return b, false, rerr
	}
	if _, err := f.Seek(-limit, io.SeekEnd); err != nil {
		return nil, false, err
	}
	b, rerr := io.ReadAll(f)
	return b, true, rerr
}

// gunzipTail streams the archive through a sliding window of limit
// bytes — memory stays bounded whatever the stream expands to — and
// refuses past maxGunzipBytes: at that point the last window is no
// longer the file's tail of anything honest.
func gunzipTail(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, false, err
	}
	defer zr.Close()
	buf := make([]byte, 64<<10)
	var tail []byte
	var total int64
	var truncated bool
	for {
		m, rerr := zr.Read(buf)
		if m > 0 {
			total += int64(m)
			if total > maxGunzipBytes {
				return nil, false, fmt.Errorf("logsink: %s decompresses past %d bytes — refusing the view (decompression bomb); download the file instead", filepath.Base(path), int64(maxGunzipBytes))
			}
			tail = append(tail, buf[:m]...)
			if int64(len(tail)) > limit {
				tail = tail[int64(len(tail))-limit:]
				truncated = true
			}
		}
		if rerr == io.EOF {
			return tail, truncated, nil
		}
		if rerr != nil {
			return nil, false, rerr
		}
	}
}
