//go:build linux && !android

package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// fakeManager is the platform's service manager as the tests model it.
type fakeManager struct {
	t     *testing.T
	home  string
	state string
	first []string
}

func newFakeManager(t *testing.T, home string) *fakeManager {
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &fakeManager{t: t, home: home, state: fakeSystemd(t, filepath.Join(home, ".config", "systemd", "user"), true)}
}

// snapshot is what is registered: every file and link under the user unit
// directory, and the units the manager runs.
func (m *fakeManager) snapshot() []string {
	var out []string
	dir := filepath.Join(m.home, ".config", "systemd", "user")
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, "unit "+rel)
		}
		return nil
	})
	running, _ := os.ReadDir(m.state)
	for _, e := range running {
		if e.Name() != "calls" {
			out = append(out, "active "+e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func (m *fakeManager) calls() []string { return managerCalls(m.t, m.state) }
