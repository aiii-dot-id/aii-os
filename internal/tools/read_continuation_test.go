package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestAContinuationTheReadToolOffersIsOneItTakes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for i := 0; i < maxReadOffset+2; i++ {
		w.WriteString("x\n")
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	rt := &ReadTool{maxBytes: 51200}
	read := func(args map[string]any) Result {
		t.Helper()
		args["file_path"] = path
		res, err := rt.Execute(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	offered := regexp.MustCompile(`continue at offset (\d+)`)
	// Whatever a page offers, the tool must take.
	taken := func(res Result) {
		t.Helper()
		m := offered.FindStringSubmatch(res.Output)
		if m == nil {
			return
		}
		next, _ := strconv.Atoi(m[1])
		if again := read(map[string]any{"offset": float64(next), "limit": float64(1)}); again.Error != "" {
			t.Fatalf("THE TOOL OFFERED A CONTINUATION IT REFUSES: %q, and then: %s", m[0], again.Error)
		}
	}

	// One line before the ceiling: the next is offered, and taken.
	before := read(map[string]any{"offset": float64(maxReadOffset - 1), "limit": float64(1)})
	if want := fmt.Sprintf("continue at offset %d", maxReadOffset); !strings.Contains(before.Output, want) {
		t.Fatalf("a page that ends before the last line the tool reads does not say %q: %q", want, before.Output)
	}
	taken(before)

	// At the ceiling, by a limit of lines.
	at := read(map[string]any{"offset": float64(maxReadOffset - 1), "limit": float64(2)})
	taken(at)
	if !strings.Contains(at.Output, fmt.Sprintf("the file goes on, past line %d", maxReadOffset)) || !strings.Contains(at.Output, "read the rest another way") {
		t.Fatalf("a page that ends at the last line the tool reads does not say the file goes on and how to read the rest: %q", at.Output[len(at.Output)-min(len(at.Output), 200):])
	}

	// At the ceiling, by the byte cap: a tool that holds two lines a page.
	small := &ReadTool{maxBytes: 20}
	res, err := small.Execute(context.Background(), map[string]any{"file_path": path, "offset": float64(maxReadOffset - 1)})
	if err != nil {
		t.Fatal(err)
	}
	if m := offered.FindStringSubmatch(res.Output); m != nil {
		if next, _ := strconv.Atoi(m[1]); next > maxReadOffset {
			t.Fatalf("a page cut at the byte cap offers a line past the ceiling: %q", m[0])
		}
	}
}

// THE SAME INSIDE ONE LONG LINE. A line read in windows names the byte the
// next window begins at. Where that byte is past the last of a line the
// tool reads, the window says so in place of naming it.
func TestAWindowOfALongLineOffersNoByteTheToolRefuses(t *testing.T) {
	rt := &ReadTool{maxBytes: 64}
	win := bytes.Repeat([]byte("y"), 65) // one more than the cap: the line goes on
	inside, err := rt.pageWindow(bufio.NewReader(strings.NewReader("")), win, false, 3, maxByteOffset-1000)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("Continue with offset 3 and byte_offset %d.", maxByteOffset-1000+64); !strings.Contains(inside.Output, want) {
		t.Fatalf("a window that ends inside what the tool reads does not say %q: %q", want, inside.Output)
	}
	// The last byte the tool takes is still offered.
	last, err := rt.pageWindow(bufio.NewReader(strings.NewReader("")), win, false, 3, maxByteOffset-64)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("byte_offset %d.", maxByteOffset); !strings.Contains(last.Output, want) {
		t.Fatalf("the last byte the tool takes is not offered (%q): %q", want, last.Output)
	}
	past, err := rt.pageWindow(bufio.NewReader(strings.NewReader("")), win, false, 3, maxByteOffset-10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(past.Output, "byte_offset") {
		t.Fatalf("THE TOOL OFFERED A BYTE IT REFUSES: %q", past.Output)
	}
	if !strings.Contains(past.Output, fmt.Sprintf("The line goes on, past byte %d", maxByteOffset)) || !strings.Contains(past.Output, "read the rest another way") {
		t.Fatalf("a window that ends past the last byte the tool reads does not say so: %q", past.Output)
	}
}

// AND AT THE END OF A LINE READ IN WINDOWS. The last window of a long line
// names the next line; where that line is past the last the tool reads,
// it says the file goes on.
func TestTheEndOfALongLineOffersNoLineTheToolRefuses(t *testing.T) {
	rt := &ReadTool{maxBytes: 64}
	more := func() *bufio.Reader { return bufio.NewReader(strings.NewReader("another line\n")) }
	before, err := rt.pageWindow(more(), []byte("the end of it"), true, maxReadOffset-1, 128)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("continue at offset %d", maxReadOffset); !strings.Contains(before.Output, want) {
		t.Fatalf("the end of a line before the last the tool reads does not say %q: %q", want, before.Output)
	}
	at, err := rt.pageWindow(more(), []byte("the end of it"), true, maxReadOffset, 128)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(at.Output, "continue at offset") || !strings.Contains(at.Output, fmt.Sprintf("the file goes on, past line %d", maxReadOffset)) {
		t.Fatalf("the end of the last line the tool reads offers a line it refuses, or does not say the file goes on: %q", at.Output)
	}
}
