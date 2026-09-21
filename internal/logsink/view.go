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

// .
type File struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Modified   string `json:"modified"`
	Compressed bool   `json:"compressed"`
}

// .
func (s *Sink) List() ([]File, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
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
	// .
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

// .
// .
// .
// .
const tailReadBytes = 1 << 20

// .
// .
// .
// .
const maxGunzipBytes = 64 << 20

// .
// .
// .
// .
// .
// .
func (s *Sink) Tail(name string, n int) ([]string, error) {
	if s == nil || s.dir == "" {
		return nil, fmt.Errorf("logsink: file logging is disabled")
	}
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
		// .
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

// .
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

// .
// .
// .
// .
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
