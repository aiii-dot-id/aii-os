package app

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

// .
// .
// .
// .
type errEntry struct {
	name string
	dir  bool
}

func (e errEntry) Name() string      { return e.name }
func (e errEntry) IsDir() bool       { return e.dir }
func (e errEntry) Type() fs.FileMode { return 0 }
func (e errEntry) Info() (fs.FileInfo, error) {
	return nil, errors.New("stat: no such file or directory")
}

// .
type okEntry struct {
	name string
	dir  bool
	size int64
}

func (e okEntry) Name() string               { return e.name }
func (e okEntry) IsDir() bool                { return e.dir }
func (e okEntry) Type() fs.FileMode          { return 0 }
func (e okEntry) Info() (fs.FileInfo, error) { return okInfo(e), nil }

type okInfo okEntry

func (i okInfo) Name() string       { return i.name }
func (i okInfo) Size() int64        { return i.size }
func (i okInfo) Mode() fs.FileMode  { return 0 }
func (i okInfo) ModTime() time.Time { return time.Time{} }
func (i okInfo) IsDir() bool        { return i.dir }
func (i okInfo) Sys() any           { return nil }

// .
// .
// .
// .
func TestWorkspaceFilesKeepsUnstattableEntry(t *testing.T) {
	entries := []os.DirEntry{
		okEntry{name: "README.md", size: 120},
		errEntry{name: "vanishing.txt"},
		okEntry{name: "sub", dir: true, size: 4096},
	}

	files := workspaceFiles(entries)

	if len(files) != 3 {
		t.Fatalf("listing lost an entry it could not stat: got %d files, want 3: %+v", len(files), files)
	}

	byName := map[string]int{}
	for i, f := range files {
		byName[f.Name] = i
	}

	i, ok := byName["vanishing.txt"]
	if !ok {
		t.Fatalf("unstattable entry was dropped from the listing: %+v", files)
	}
	if got := files[i].Size; got != 0 {
		t.Errorf("unstattable entry reported a size it could not know: got %d, want 0", got)
	}

	// .
	if got := files[byName["README.md"]].Size; got != 120 {
		t.Errorf("stattable file size: got %d, want 120", got)
	}
	if !files[byName["sub"]].Dir {
		t.Errorf("directory kind lost for %q", "sub")
	}
	if files[byName["README.md"]].Dir {
		t.Errorf("plain file reported as a directory")
	}
}

// .
// .
func TestWorkspaceFilesEmptyStaysEmpty(t *testing.T) {
	if files := workspaceFiles(nil); len(files) != 0 {
		t.Fatalf("empty directory produced entries: %+v", files)
	}
}
