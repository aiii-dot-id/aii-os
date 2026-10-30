package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/limits"
)

type ReadTool struct {
	maxBytes int

	held func(ctx context.Context, path string, file os.FileInfo, by *os.File) error
}

const readPageRoom = 512

func (t *ReadTool) page(ctx context.Context) int {
	n := limits.ToolResultChars(ctx) - readPageRoom
	if t.maxBytes > 0 && t.maxBytes < n {
		n = t.maxBytes
	}
	return max(n, utf8.UTFMax)
}

func (t *ReadTool) Name() string { return "read" }
func (t *ReadTool) Description() string {
	return "Read a file as numbered lines (cat -n). Each line number and a tab are not in the file: leave them out of edit's old_string. A truncated result says where to continue."
}

func readImage(path, mediaType string, width, height int, size int64) Result {
	return Result{Output: fmt.Sprintf("%s is an image: %s, %d by %d pixels, %d bytes.", path, mediaType, width, height, size),
		Image: &ResultImage{Path: path, MediaType: mediaType, Width: width, Height: height}}
}

func readMedia(path, mediaType, kind string, size int64) Result {
	noun := map[string]string{"pdf": "a PDF", "audio": "a sound", "video": "a video"}[kind]
	return Result{Output: fmt.Sprintf("%s is %s: %s, %d bytes.", path, noun, mediaType, size),
		Image: &ResultImage{Path: path, MediaType: mediaType}}
}

func (t *ReadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path":   map[string]any{"type": "string"},
			"offset":      map[string]any{"type": "integer", "description": "First line, 1-based"},
			"limit":       map[string]any{"type": "integer", "description": "Most lines"},
			"byte_offset": map[string]any{"type": "integer", "description": "Continue inside a long line, as a truncated result says"},
		},
		"required": []string{"file_path"},
	}
}

const (
	maxReadOffset = 10_000_000
	maxReadLimit  = 1_000_000
	maxByteOffset = 1 << 30
)

func nextLine(next int) string {
	if next > maxReadOffset {
		return fmt.Sprintf("the file goes on, past line %d, which is the last this tool reads: read the rest another way, such as a shell tool", maxReadOffset)
	}
	return fmt.Sprintf("continue at offset %d", next)
}

func (t *ReadTool) Execute(ctx context.Context, args map[string]any) (Result, error) {
	path, _ := args["file_path"].(string)
	if path == "" {
		return Refusal(ReasonArgumentsRequired, "file_path is required"), nil
	}
	offset, err := intArg(args, "offset", 1)
	if err != nil {
		return failed(ReasonArgumentInvalid, "offset: "+err.Error()), nil
	}
	if offset < 1 {
		return failed(ReasonArgumentInvalid, fmt.Sprintf("offset must be 1 or greater, got %d", offset)), nil
	}
	if offset > maxReadOffset {
		return failed(ReasonArgumentInvalid, fmt.Sprintf("offset %d is beyond the %d-line ceiling", offset, maxReadOffset)), nil
	}
	byteOffset, err := intArg(args, "byte_offset", 0)
	if err != nil {
		return failed(ReasonArgumentInvalid, "byte_offset: "+err.Error()), nil
	}
	if byteOffset < 0 {
		return failed(ReasonArgumentInvalid, fmt.Sprintf("byte_offset must not be negative, got %d", byteOffset)), nil
	}
	if byteOffset > maxByteOffset {
		return failed(ReasonArgumentInvalid, fmt.Sprintf("byte_offset %d is beyond the %d-byte ceiling", byteOffset, maxByteOffset)), nil
	}
	limit, err := intArg(args, "limit", 0)
	if err != nil {
		return failed(ReasonArgumentInvalid, "limit: "+err.Error()), nil
	}
	if limit < 0 {
		return failed(ReasonArgumentInvalid, fmt.Sprintf("limit must not be negative, got %d", limit)), nil
	}
	if limit > maxReadLimit {
		return failed(ReasonArgumentInvalid, fmt.Sprintf("limit %d is beyond the %d-line ceiling — the byte cap would truncate long before", limit, maxReadLimit)), nil
	}

	f, st, err := openRegular(path)
	if err != nil {
		return Result{Error: osRefused(err), ReasonCode: osReason(err)}, nil
	}
	defer f.Close()

	if t.held != nil {
		if kept := t.held(ctx, path, st, f); kept != nil {
			return Refusal(ReasonPathRefused, "access denied: "+kept.Error()), nil
		}
	}
	if mediaType, width, height, ok := ImageConfig(f); ok {
		return readImage(path, mediaType, width, height, st.Size()), nil
	}
	if mediaType, kind := MediaOf(f); kind != "" {
		return readMedia(path, mediaType, kind, st.Size()), nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return failedOn(err), nil
	}

	r := bufio.NewReaderSize(f, 64<<10)
	skipped, err := skipLines(ctx, r, offset-1)
	if err != nil {
		return failedOn(err), nil
	}
	if skipped < offset-1 {
		return failed(ReasonOffsetPastEnd, fmt.Sprintf(
			"offset %d is past the end of %s, which has %d line(s)", offset, path, skipped)), nil
	}
	page := t.page(ctx)

	if byteOffset > 0 {
		win, ended, endedBefore, err := lineWindow(ctx, r, byteOffset, page+1)
		if err != nil {
			return failedOn(err), nil
		}
		if endedBefore == 0 && len(win) == 0 && !ended {
			return failed(ReasonOffsetPastEnd, fmt.Sprintf(
				"offset %d is past the end of %s, which has %d line(s)", offset, path, skipped)), nil
		}
		if endedBefore > 0 || (endedBefore == 0 && ended && len(win) == 0 && byteOffset > 0) {
			return failed(ReasonOffsetPastEnd, fmt.Sprintf(
				"byte_offset %d is past the end of line %d, which is %d byte(s) long",
				byteOffset, offset, endedBefore)), nil
		}
		return t.pageWindow(r, win, ended, offset, byteOffset, page)
	}

	var out strings.Builder
	kept := 0
	cappedOut := false
	for {
		if err := ctx.Err(); err != nil {
			return failed(ReasonCancelled, "read cancelled: "+err.Error()), nil
		}
		if limit > 0 && kept == limit {
			break
		}
		win, ended, _, err := lineWindow(ctx, r, 0, page+1)
		if err != nil {
			return failedOn(err), nil
		}
		if !ended && len(win) == 0 {

			if kept == 0 && offset == 1 {
				return Result{Output: "[empty file]"}, nil
			}
			if kept == 0 {
				return failed(ReasonOffsetPastEnd, fmt.Sprintf(
					"offset %d is past the end of %s, which has %d line(s)", offset, path, skipped)), nil
			}
			break
		}
		if !ended || len(win) > page {

			if kept == 0 {
				return t.pageWindow(r, win, ended, offset, 0, page)
			}
			cappedOut = true
			break
		}

		num := lineNumber(offset + kept)
		if kept > 0 && out.Len()+1+len(num)+len(win) > page {
			cappedOut = true
			break
		}
		if kept > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(num)
		out.Write(win)
		kept++
	}
	if cappedOut {
		return Result{
			Output:    out.String() + fmt.Sprintf("\n[truncated at the byte cap — %s]", nextLine(offset+kept)),
			Truncated: true,
		}, nil
	}
	if limit > 0 && kept == limit && hasAnotherLine(r) {

		return Result{Output: out.String() + fmt.Sprintf("\n[more lines — %s]", nextLine(offset+kept))}, nil
	}
	return Result{Output: out.String()}, nil
}

var errNotRegular = errors.New("not a regular file")

func openRegular(path string) (*os.File, os.FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is %w (%s) — read serves files only", path, errNotRegular, st.Mode().Type())
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

func osRefused(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return err.Error() + " — the operating system refused it, not AII's wall"
	}
	return err.Error()
}

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

func (t *ReadTool) pageWindow(r *bufio.Reader, win []byte, ended bool, offset, byteOffset, page int) (Result, error) {

	if byteOffset > 0 && len(win) > 0 && !utf8.RuneStart(win[0]) {
		return failed(ReasonArgumentInvalid, fmt.Sprintf(
			"byte_offset %d is inside a UTF-8 character on line %d — use the offset a previous page reported",
			byteOffset, offset)), nil
	}
	if ended && len(win) <= page {
		out := lineNumber(offset) + string(win)
		if hasAnotherLine(r) {
			out += fmt.Sprintf("\n[end of line %d — %s]", offset, nextLine(offset+1))
		}
		return Result{Output: out}, nil
	}
	cut := page
	if cut > len(win) {
		cut = len(win)
	}
	for cut > 0 && cut < len(win) && !utf8.RuneStart(win[cut]) {
		cut--
	}
	next := byteOffset + cut
	hint := fmt.Sprintf("Continue with offset %d and byte_offset %d.", offset, next)
	if next > maxByteOffset {
		hint = fmt.Sprintf("The line goes on, past byte %d, which is the last of a line this tool reads: read the rest another way, such as a shell tool.", maxByteOffset)
	}
	return Result{
		Output: lineNumber(offset) + string(win[:cut]) + fmt.Sprintf(
			"\n[line %d continues: bytes %d–%d shown. %s]", offset, byteOffset, next, hint),
		Truncated: true,
	}, nil
}

func lineNumber(n int) string { return fmt.Sprintf("%6d\t", n) }

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

func hasAnotherLine(r *bufio.Reader) bool {
	_, err := r.Peek(1)
	return err == nil
}

func intArg(args map[string]any, name string, def int) (int, error) {
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

		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("must be a whole number, got %q", n)
		}
		return parsed, nil
	}
	return 0, fmt.Errorf("must be a whole number, got %T", v)
}

func boolArg(args map[string]any, name string) (bool, error) {
	switch v := args[name].(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case string:
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b, nil
		}
	}
	return false, fmt.Errorf("%s must be true or false, got %v", name, args[name])
}

func stringArg(args map[string]any, name string) (string, error) {
	switch v := args[name].(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	}
	return "", fmt.Errorf("%s must be a string, got %T", name, args[name])
}

func (t *ReadTool) ReadOnly() bool { return true }
