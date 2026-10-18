//go:build linux || darwin

package pluginhost_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// The machine service's link, tried from inside a wall. An external test:
// the machine service imports this package (it is the plugin store's
// writer), so a test that takes the service's own link cannot be one of
// this package's own.

// machineLink is the machine service's own link for a test: a service
// directory under root, the account's runtime directory pointed there too
// on Linux (where the link lies), taken through machine.Listen exactly as
// the service takes it, with a count of what its listener accepts. The
// places are what the app hands every native child's wall for it.
func machineLink(t *testing.T) (h *machine.Holder, dir string, accepted func() int, places []*firewall.Rule) {
	t.Helper()
	root := pluginhost.ShortDirForTest(t)
	if runtime.GOOS == "linux" {
		rt := filepath.Join(root, "run")
		if err := os.Mkdir(rt, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", rt)
	}
	dir = filepath.Join(root, "machine")
	h, err := machine.Listen(dir)
	if err != nil {
		t.Fatalf("the service's own link: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	var mu sync.Mutex
	n := 0
	go func() {
		for {
			c, err := h.Listener().Accept()
			if err != nil {
				return
			}
			mu.Lock()
			n++
			mu.Unlock()
			c.Close()
		}
	}()
	accepted = func() int {
		time.Sleep(250 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return n
	}
	places = []*firewall.Rule{{ID: "sub.machine", Kind: firewall.KindSubstrate, Path: dir, Enforced: true}}
	return h, dir, accepted, places
}

// THE MACHINE SERVICE'S LINK IS OUT OF EVERY WALL'S REACH, at the path the
// service listens on and with its directory among the places the app
// masks. Uncontained the child connects and lists the directory; inside
// the wall the connection is refused (Linux: no socket can be made, and
// the runtime directory the link lies in is hidden; macOS: no socket on
// the file system is reached, and the directory is masked) and the
// listener accepts nothing.
func TestAContainedNativeChildCannotReachTheMachineLink(t *testing.T) {
	pluginhost.RequireWallForTest(t)
	h, dir, accepted, places := machineLink(t)
	asks := []string{pluginhost.FakechildForTest(), "reach", "unix=" + h.Path(), "list=" + dir}
	out := pluginhost.RunWallProbeForTest(t, asks)
	if !strings.Contains(out, "unix=connected") || strings.Contains(out, "list=refused") || strings.Contains(out, "list=0") || accepted() != 1 {
		t.Fatalf("rig: uncontained, the probe must connect to the link and list the service's directory, or their refusal below proves nothing:\n%s", out)
	}
	argv, wall, err := pluginhost.ContainForTest(context.Background(), asks, places)
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out = pluginhost.RunContainedForTest(t, argv, wall)
	if !strings.Contains(out, "unix=refused") {
		t.Errorf("a contained child connected to the machine service's link:\n%s", out)
	}
	want := map[string]string{"linux": "list=0", "darwin": "list=refused"}[runtime.GOOS]
	if !strings.Contains(out, want) {
		t.Errorf("the machine service's directory is not masked inside the wall (want %q):\n%s", want, out)
	}
	if n := accepted(); n != 1 {
		t.Errorf("the link accepted %d connections; only the uncontained probe's is expected", n)
	}
	t.Logf("inside the wall: %s", strings.Join(strings.Fields(out), " "))
}
