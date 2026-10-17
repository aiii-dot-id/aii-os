package app

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

// errEntry is a directory entry whose Info() fails — the real case being
// a file unlinked between the ReadDir and the stat. It cannot be produced
// deterministically on a live filesystem, which is exactly why the
// conversion is a separate function: the failure is reachable here.
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

// okEntry is a directory entry that stats cleanly.
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

// TestWorkspaceFilesKeepsUnstattableEntry pins the inertness property of
// the file listing: an entry the projection cannot stat is reported with
// what is known, never dropped. A dropped entry is a silent omission —
// the reader would take the absence as a fact about the project.
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

	// The known facts of the other entries must survive unchanged.
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

// TestWorkspaceFilesEmptyStaysEmpty guards the wire shape: an empty
// project directory must not become a non-empty listing.
func TestWorkspaceFilesEmptyStaysEmpty(t *testing.T) {
	if files := workspaceFiles(nil); len(files) != 0 {
		t.Fatalf("empty directory produced entries: %+v", files)
	}
}
