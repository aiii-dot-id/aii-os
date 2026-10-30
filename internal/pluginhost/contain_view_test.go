//go:build linux || darwin

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The engine the row-9 tests contain (contain_view_linux_test.go,
// contain_view_darwin_test.go): the suite's stand-in, copied into a
// directory of its own as a package's artifact is, beside places it is not
// handed.

// engineLayout is an engine's material and two places it is not handed, all
// in one directory the account owns: its code (own), its models (models),
// its private directory (private), a credential of the account's (account)
// and a project of the account's (project), each holding one file.
type engineLayout struct {
	child                                     string
	own, models, private, account, project    string
	ownFile, model, state, credential, sketch string
}

func newEngineLayout(t *testing.T) engineLayout {
	t.Helper()
	root := shortDir(t)
	l := engineLayout{own: filepath.Join(root, "package"), models: filepath.Join(root, "models"), private: filepath.Join(root, "private"),
		account: filepath.Join(root, "account", ".aws"), project: filepath.Join(root, "project")}
	l.child = filepath.Join(l.own, "child")
	l.ownFile = filepath.Join(l.own, "engine.json")
	l.model = filepath.Join(l.models, "weights.bin")
	l.state = filepath.Join(l.private, "state")
	l.credential = filepath.Join(l.account, "credentials")
	l.sketch = filepath.Join(l.project, "notes.txt")
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{l.child: raw, l.ownFile: []byte("{}"), l.model: []byte("weights"), l.state: []byte("state"),
		l.credential: []byte("a credential"), l.sketch: []byte("a project")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// files is what the spawn hands the wall for this engine: its code as its
// own, its models read, its private directory.
func (l engineLayout) files() nativeFiles {
	return nativeFiles{own: []string{l.own}, read: []string{l.models}, private: l.private}
}

// probe runs the engine's reach probe uncontained, asserting that every
// line in control is there, then behind the wall built from files, and
// returns what it reported there.
func (l engineLayout) probe(t *testing.T, files nativeFiles, asks []string, control []string) string {
	t.Helper()
	out := runWallProbe(t, append([]string{l.child, "reach"}, asks...))
	for _, want := range control {
		if !strings.Contains(out, want) {
			t.Fatalf("control: uncontained, the engine must report %q, or its refusal behind the wall proves nothing:\n%s", want, out)
		}
	}
	return l.contained(t, files, asks)
}

// contained runs the engine's reach probe behind the wall built from files.
func (l engineLayout) contained(t *testing.T, files nativeFiles, asks []string) string {
	t.Helper()
	wrapped, wall, err := containArgv(context.Background(), append([]string{l.child, "reach"}, asks...), nil, files)
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	return runContained(t, wrapped, wall)
}
