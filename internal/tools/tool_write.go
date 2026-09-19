package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// .
// .

type WriteTool struct{}

func (t *WriteTool) Name() string { return "write" }
func (t *WriteTool) Description() string {
	return "Write content to a file (overwrites). Args: file_path (required), content (required)"
}

func (t *WriteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{"type": "string", "description": "Path to the file to write"},
			"content":   map[string]interface{}{"type": "string", "description": "Content to write"},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	path, _ := args["file_path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return Result{Error: "file_path is required"}, nil
	}

	// .
	// .
	// .
	// .
	// .
	if err := ctx.Err(); err != nil {
		return Result{Error: err.Error()}, nil
	}
	measured, prevBytes, prevLines, prevErr := previousFile(ctx, path)
	if errors.Is(prevErr, errReceiptNotRegular) {
		return Result{Error: prevErr.Error()}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{Error: err.Error()}, nil
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
	// .
	same, err := writeFileNoFollowSame(path, []byte(content), 0644, measured)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if !same && (prevErr == nil || errors.Is(prevErr, fs.ErrNotExist)) {
		prevErr = errReceiptMoved
	}

	return Result{Output: fmt.Sprintf("Wrote %d bytes to %s %s", len(content), path,
		replacedNote(prevBytes, prevLines, prevErr, content))}, nil
}

// .
// .
const writeReceiptMaxBytes int64 = 1 << 20

var errReceiptNotRegular = errors.New("write receipt requires a regular file")

// .
// .
var errReceiptMoved = errors.New("the file at this path changed between measuring it and writing; counts unavailable")

// .
// .
// .
// .
// .
// .
func previousFile(ctx context.Context, path string) (os.FileInfo, int64, int, error) {
	f, err := openWriteReceipt(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	if !st.Mode().IsRegular() {
		return nil, 0, 0, errReceiptNotRegular
	}
	if st.Size() > writeReceiptMaxBytes {
		return st, 0, 0, fmt.Errorf("receipt scan exceeds %d bytes; counts unavailable", writeReceiptMaxBytes)
	}
	reader := io.LimitReader(f, writeReceiptMaxBytes+1)
	buf := make([]byte, 32*1024)
	var size int64
	var lines int
	var tail byte
	for {
		if err := ctx.Err(); err != nil {
			return st, 0, 0, err
		}
		n, rerr := reader.Read(buf)
		size += int64(n)
		if size > writeReceiptMaxBytes {
			return st, 0, 0, fmt.Errorf("receipt scan exceeds %d bytes; counts unavailable", writeReceiptMaxBytes)
		}
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			tail = buf[n-1]
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return st, 0, 0, rerr
		}
	}
	if size > 0 && tail != '\n' {
		lines++
	}
	return st, size, lines, nil
}

// .
// .
// .
// .
func replacedNote(prevBytes int64, prevLines int, prevErr error, content string) string {
	switch {
	case errors.Is(prevErr, fs.ErrNotExist):
		return "(new file)"
	case prevErr != nil:
		return fmt.Sprintf("(replaced a file that could not be read: %v)", prevErr)
	}
	delta, lines := lineCount(content)-prevLines, "±0 lines"
	switch {
	case delta > 0:
		lines = fmt.Sprintf("+%d lines", delta)
	case delta < 0:
		lines = fmt.Sprintf("−%d lines", -delta)
	}
	return fmt.Sprintf("(replaced %d bytes; %s)", prevBytes, lines)
}

// .
// .
func lineCount(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}

// .
