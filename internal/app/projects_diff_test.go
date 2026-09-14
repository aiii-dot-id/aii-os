package app

import "testing"

// .
// .
func TestProjectsDiffManifestEditRefreshesTheDock(t *testing.T) {
	oldM := map[string]slugSnap{
		"foo": {manifest: "project.json 100 10", digest: "note.md 5 5\nproject.json 100 10"},
	}
	newM := map[string]slugSnap{
		"foo": {manifest: "project.json 200 12", digest: "note.md 5 5\nproject.json 200 12"},
	}
	moved, listChanged := projectsDiffMaps(oldM, newM)
	if len(moved) != 1 || moved[0] != "foo" {
		t.Fatalf("moved = %v, want [foo]", moved)
	}
	if !listChanged {
		t.Fatal("a manifest edit to an EXISTING project did not refresh the dock — the shipped prefix check could never match \"slug/project.json\"")
	}
}

func TestProjectsDiffPrefixSlugsDoNotCrossAttribute(t *testing.T) {
	oldM := map[string]slugSnap{
		"foo":     {manifest: "project.json 1 1", digest: "note.md 1 1\nproject.json 1 1"},
		"foo-bar": {manifest: "project.json 1 1", digest: "project.json 1 1"},
	}
	newM := map[string]slugSnap{
		"foo":     {manifest: "project.json 1 1", digest: "note.md 9 9\nproject.json 1 1"},
		"foo-bar": {manifest: "project.json 1 1", digest: "project.json 1 1"},
	}
	moved, listChanged := projectsDiffMaps(oldM, newM)
	if len(moved) != 1 || moved[0] != "foo" {
		t.Fatalf("moved = %v, want exactly [foo] — the sorted-lines parser attributed foo\u0027s files to foo-bar", moved)
	}
	if listChanged {
		t.Fatal("a non-manifest edit flagged the dock")
	}
}
