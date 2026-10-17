//go:build darwin && !ios

package install

import (
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
	return &fakeManager{t: t, home: home, state: fakeLaunchd(t)}
}

// snapshot is what is registered: the agents in the home and the labels
// launchd holds.
func (m *fakeManager) snapshot() []string {
	var out []string
	entries, _ := os.ReadDir(filepath.Join(m.home, agentDirRel))
	for _, e := range entries {
		out = append(out, "plist "+e.Name())
	}
	held, _ := os.ReadDir(m.state)
	for _, e := range held {
		if e.Name() != "calls" {
			out = append(out, "held "+e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func (m *fakeManager) calls() []string { return calls(m.t, m.state) }
