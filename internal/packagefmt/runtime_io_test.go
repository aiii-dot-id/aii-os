package packagefmt

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"
)

type runtimeFailingWriter struct{ cause error }

func (w runtimeFailingWriter) Write([]byte) (int, error) { return 0, w.cause }

func TestRuntimePayloadPreservesOutputIOCause(t *testing.T) {
	archive, _ := buildTree(t, "runtime", treeFixture, nil)
	limits := (TreeLimits{}).filled().tarLimits()
	gz, err := newGzipStreamWith(bytes.NewReader(archive), limits)
	if err != nil {
		t.Fatal(err)
	}
	walker := newTarWalkerWith(gz, limits)
	root, _, err := walker.next()
	if err != nil || !root.isDir {
		t.Fatalf("root: %v", err)
	}
	member, _, err := walker.next()
	if err != nil || member.isDir {
		t.Fatalf("inventory: %v", err)
	}
	fault := &fs.PathError{Op: "write", Path: "synthetic", Err: errors.New("disk unavailable")}
	if err := walker.readPayload(member, runtimeFailingWriter{fault}); !errors.Is(err, fault) {
		t.Fatalf("disk error became an unclassifiable grammar message: %v", err)
	}
}
