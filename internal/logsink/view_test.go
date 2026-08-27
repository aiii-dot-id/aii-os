package logsink

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A live log bigger than the read cap must not be loaded whole: the
// view reads from the END, and the window the caller asked for is
// intact (D49, Sev 2026-08-26).
func TestTailReadsOnlyTheEnd(t *testing.T) {
	dir := t.TempDir()
	s := &Sink{dir: dir}
	f, err := os.Create(filepath.Join(dir, LiveName))
	if err != nil {
		t.Fatal(err)
	}
	const total = 170000
	for i := 0; i < total; i++ {
		fmt.Fprintf(f, "line-%07d\n", i)
	}
	f.Close()

	lines, err := s.Tail(LiveName, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 400 {
		t.Fatalf("want 400 lines, got %d", len(lines))
	}
	if want := fmt.Sprintf("line-%07d", total-1); lines[len(lines)-1] != want {
		t.Fatalf("last line %q, want %q — the view is not the end of the log", lines[len(lines)-1], want)
	}
	if want := fmt.Sprintf("line-%07d", total-400); lines[0] != want {
		t.Fatalf("first line %q, want %q", lines[0], want)
	}
}

// When the byte cap eats into the requested window, the view says so
// and names the route — never a silent shortfall (R18).
func TestTailDeclaresTheCutWhenItEatsTheWindow(t *testing.T) {
	dir := t.TempDir()
	s := &Sink{dir: dir}
	f, err := os.Create(filepath.Join(dir, LiveName))
	if err != nil {
		t.Fatal(err)
	}
	wide := strings.Repeat("x", 8<<10)
	for i := 0; i < 400; i++ {
		fmt.Fprintf(f, "%s-%03d\n", wide, i)
	}
	f.Close()

	lines, err := s.Tail(LiveName, 400)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lines[0], "not shown") {
		t.Fatalf("cut window carries no declaration: %q", lines[0][:40])
	}
	if !strings.HasSuffix(lines[len(lines)-1], "-399") {
		t.Fatalf("view does not end at the log's end: %q", lines[len(lines)-1][len(lines[len(lines)-1])-8:])
	}
}

// A gzip that expands without end is refused with the route to the
// real bytes — the view must not be the identity's memory ceiling.
func TestGzipBombIsRefused(t *testing.T) {
	dir := t.TempDir()
	s := &Sink{dir: dir}
	name := rotatedPrefix + "20260101-000000" + rotatedExt + gzipExt
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	zeros := make([]byte, 1<<20)
	for i := 0; i < 100; i++ { // 100 MiB decompressed, tiny on disk
		if _, err := zw.Write(zeros); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	f.Close()

	_, err = s.Tail(name, 400)
	if err == nil {
		t.Fatal("a decompression bomb was viewed")
	}
	if !strings.Contains(err.Error(), "decompression bomb") {
		t.Fatalf("refusal does not name the cause: %v", err)
	}
}

// An ordinary rotated archive still views normally.
func TestGzipTailNormal(t *testing.T) {
	dir := t.TempDir()
	s := &Sink{dir: dir}
	name := rotatedPrefix + "20260101-000001" + rotatedExt + gzipExt
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	for i := 0; i < 500; i++ {
		fmt.Fprintf(zw, "z-%03d\n", i)
	}
	zw.Close()
	f.Close()

	lines, err := s.Tail(name, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 400 || lines[len(lines)-1] != "z-499" {
		t.Fatalf("gzip view wrong: %d lines, last %q", len(lines), lines[len(lines)-1])
	}
}
