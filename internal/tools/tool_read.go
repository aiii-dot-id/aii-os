package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// .
// .

type ReadTool struct{ maxBytes int }

func (t *ReadTool) Name() string { return "read" }
func (t *ReadTool) Description() string {
	return "Read file contents. Args: file_path (required), offset (1-based first line, optional), limit (max lines, optional), byte_offset (continue INSIDE a long line, optional). A truncated result names exactly where to continue — the next line offset, or the next byte_offset when one line is longer than the cap."
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
func (t *ReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path":   map[string]interface{}{"type": "string", "description": "Path to the file to read"},
			"offset":      map[string]interface{}{"type": "integer", "description": "1-based line to start at (default 1)"},
			"limit":       map[string]interface{}{"type": "integer", "description": "Maximum number of lines to return (default: to end of file)"},
			"byte_offset": map[string]interface{}{"type": "integer", "description": "Byte position WITHIN the line at offset, to continue a line longer than the cap (default 0). Use the value a previous truncated result reported; boundaries are UTF-8 safe."},
		},
		"required": []string{"file_path"},
	}
}

// .
// .
// .
// .
// .
// .
const (
	maxReadOffset = 10_000_000
	maxReadLimit  = 1_000_000
	maxByteOffset = 1 << 30
)

func (t *ReadTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	path, _ := args["file_path"].(string)
	if path == "" {
		return Result{Error: "file_path is required"}, nil
	}
	offset, err := intArg(args, "offset", 1)
	if err != nil {
		return Result{Error: "offset: " + err.Error()}, nil
	}
	if offset < 1 {
		return Result{Error: fmt.Sprintf("offset must be 1 or greater, got %d", offset)}, nil
	}
	if offset > maxReadOffset {
		return Result{Error: fmt.Sprintf("offset %d is beyond the %d-line ceiling", offset, maxReadOffset)}, nil
	}
	byteOffset, err := intArg(args, "byte_offset", 0)
	if err != nil {
		return Result{Error: "byte_offset: " + err.Error()}, nil
	}
	if byteOffset < 0 {
		return Result{Error: fmt.Sprintf("byte_offset must not be negative, got %d", byteOffset)}, nil
	}
	if byteOffset > maxByteOffset {
		return Result{Error: fmt.Sprintf("byte_offset %d is beyond the %d-byte ceiling", byteOffset, maxByteOffset)}, nil
	}
	limit, err := intArg(args, "limit", 0)
	if err != nil {
		return Result{Error: "limit: " + err.Error()}, nil
	}
	if limit < 0 {
		return Result{Error: fmt.Sprintf("limit must not be negative, got %d", limit)}, nil
	}
	if limit > maxReadLimit {
		return Result{Error: fmt.Sprintf("limit %d is beyond the %d-line ceiling — the byte cap would truncate long before", limit, maxReadLimit)}, nil
	}

	f, st, err := openRegular(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	defer f.Close()

	// .
	// .
	// .
	// .
	// .
	if offset == 1 && limit == 0 && byteOffset == 0 && st.Size() <= int64(t.maxBytes) {
		data, err := io.ReadAll(io.LimitReader(f, int64(t.maxBytes)+1))
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		if len(data) <= t.maxBytes {
			return Result{Output: string(data)}, nil
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return Result{Error: err.Error()}, nil
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	r := bufio.NewReaderSize(f, 64<<10)
	skipped, err := skipLines(ctx, r, offset-1)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if skipped < offset-1 {
		return Result{Output: "", Error: fmt.Sprintf(
			"offset %d is past the end of %s, which has %d line(s)", offset, path, skipped)}, nil
	}
	// .
	// .
	if byteOffset > 0 {
		win, ended, endedBefore, err := lineWindow(ctx, r, byteOffset, t.maxBytes+1)
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		if endedBefore == 0 && len(win) == 0 && !ended {
			return Result{Output: "", Error: fmt.Sprintf(
				"offset %d is past the end of %s, which has %d line(s)", offset, path, skipped)}, nil
		}
		if endedBefore > 0 || (endedBefore == 0 && ended && len(win) == 0 && byteOffset > 0) {
			return Result{Error: fmt.Sprintf(
				"byte_offset %d is past the end of line %d, which is %d byte(s) long",
				byteOffset, offset, endedBefore)}, nil
		}
		return t.pageWindow(r, win, ended, offset, byteOffset)
	}

	var out strings.Builder
	kept := 0
	cappedOut := false
	for {
		if err := ctx.Err(); err != nil {
			return Result{Error: "read cancelled: " + err.Error()}, nil
		}
		if limit > 0 && kept == limit {
			break
		}
		win, ended, _, err := lineWindow(ctx, r, 0, t.maxBytes+1)
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		if !ended && len(win) == 0 {
			// .
			if kept == 0 {
				return Result{Output: "", Error: fmt.Sprintf(
					"offset %d is past the end of %s, which has %d line(s)", offset, path, skipped)}, nil
			}
			break
		}
		if !ended || len(win) > t.maxBytes {
			// .
			// .
			// .
			// .
			if kept == 0 {
				return t.pageWindow(r, win, ended, offset, 0)
			}
			cappedOut = true
			break
		}
		sep := 0
		if kept > 0 {
			sep = 1
		}
		if out.Len()+sep+len(win) > t.maxBytes {
			cappedOut = true
			break
		}
		if kept > 0 {
			out.WriteByte('\n')
		}
		out.Write(win)
		kept++
	}
	if cappedOut {
		return Result{
			Output:    out.String() + fmt.Sprintf("\n[truncated at the byte cap — continue at offset %d]", offset+kept),
			Truncated: true,
		}, nil
	}
	if limit > 0 && kept == limit && hasAnotherLine(r) {
		// .
		// .
		// .
		return Result{Output: out.String() + fmt.Sprintf("\n[more lines — continue at offset %d]", offset+kept)}, nil
	}
	return Result{Output: out.String()}, nil
}

// .
// .
// .
// .
// .
// .
// .
func openRegular(path string) (*os.File, os.FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular file (%s) — read serves files only", path, st.Mode().Type())
	}
	f, err := openNonBlocking(path)
	if err != nil {
		return nil, nil, err
	}
	fst, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !fst.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s changed type while opening (%s) — refused", path, fst.Mode().Type())
	}
	return f, fst, nil
}

// .
// .
// .
// .
// .
// .
// .
func lineWindow(ctx context.Context, r *bufio.Reader, start, max int) (win []byte, ended bool, endedBefore int, err error) {
	pos := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, 0, fmt.Errorf("read cancelled: %w", err)
		}
		chunk, rerr := r.ReadSlice('\n')
		data := chunk
		nl := len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		if nl {
			data = chunk[:len(chunk)-1]
		}
		if len(data) > 0 && pos+len(data) > start {
			from := 0
			if start > pos {
				from = start - pos
			}
			take := data[from:]
			if room := max - len(win); len(take) > room {
				win = append(win, take[:room]...)
				// .
				// .
				// .
				return win, false, 0, nil
			}
			win = append(win, take...)
		}
		pos += len(data)
		switch {
		case nl:
			if pos < start || (pos == start && start > 0) {
				return nil, true, pos, nil
			}
			return win, true, 0, nil
		case rerr == bufio.ErrBufferFull:
			continue
		case rerr == io.EOF:
			if pos == 0 && len(chunk) == 0 {
				return nil, false, 0, nil
			}
			if pos < start || (pos == start && start > 0) {
				return nil, true, pos, nil
			}
			return win, true, 0, nil
		case rerr != nil:
			return nil, false, 0, rerr
		}
	}
}

// .
// .
// .
func (t *ReadTool) pageWindow(r *bufio.Reader, win []byte, ended bool, offset, byteOffset int) (Result, error) {
	// .
	// .
	if byteOffset > 0 && len(win) > 0 && !utf8.RuneStart(win[0]) {
		return Result{Error: fmt.Sprintf(
			"byte_offset %d is inside a UTF-8 character on line %d — use the offset a previous page reported",
			byteOffset, offset)}, nil
	}
	if ended && len(win) <= t.maxBytes {
		out := string(win)
		if hasAnotherLine(r) {
			out += fmt.Sprintf("\n[end of line %d — continue at offset %d]", offset, offset+1)
		}
		return Result{Output: out}, nil
	}
	cut := t.maxBytes
	if cut > len(win) {
		cut = len(win)
	}
	for cut > 0 && cut < len(win) && !utf8.RuneStart(win[cut]) {
		cut--
	}
	next := byteOffset + cut
	return Result{
		Output: string(win[:cut]) + fmt.Sprintf(
			"\n[line %d continues: bytes %d–%d shown. Continue with offset %d and byte_offset %d.]",
			offset, byteOffset, next, offset, next),
		Truncated: true,
	}, nil
}

// .
// .
func skipLines(ctx context.Context, r *bufio.Reader, n int) (int, error) {
	skipped := 0
	for skipped < n {
		if err := ctx.Err(); err != nil {
			return skipped, fmt.Errorf("read cancelled: %w", err)
		}
		sawData := false
		for {
			b, err := r.ReadSlice('\n')
			if len(b) > 0 {
				sawData = true
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			if err == io.EOF {
				if sawData {
					skipped++
				}
				return skipped, nil
			}
			if err != nil {
				return skipped, err
			}
			break
		}
		skipped++
	}
	return skipped, nil
}

// .
func hasAnotherLine(r *bufio.Reader) bool {
	_, err := r.Peek(1)
	return err == nil
}

// .
// .
// .
// .
func intArg(args map[string]interface{}, name string, def int) (int, error) {
	v, ok := args[name]
	if !ok || v == nil {
		return def, nil
	}
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("must be a whole number, got %v", n)
		}
		return int(n), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case string:
		if n == "" {
			return def, nil
		}
		// .
		// .
		// .
		// .
		// .
		// .
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("must be a whole number, got %q", n)
		}
		return parsed, nil
	}
	return 0, fmt.Errorf("must be a whole number, got %T", v)
}

// .

// .
func (t *ReadTool) ReadOnly() bool { return true }
