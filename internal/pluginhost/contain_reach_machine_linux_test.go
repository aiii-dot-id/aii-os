//go:build linux

package pluginhost_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// A CUDA CHILD STILL REACHES NO MACHINE LINK. The one socket its filter
// admits is packet-sequenced, and the link is a stream socket: a
// connection between sockets of different types is refused by the kernel
// before anything else (EPROTOTYPE, shown uncontained, where nothing else
// stands in the way), and inside the wall the runtime directory the link
// lies in is hidden besides.
func TestACUDAChildCannotReachTheMachineLink(t *testing.T) {
	pluginhost.RequireWallForTest(t)
	h, _, accepted, places := machineLink(t)
	asks := []string{pluginhost.FakechildForTest(), "reach", "seqconnect=" + h.Path(), "unix=" + h.Path()}
	out := pluginhost.RunWallProbeForTest(t, asks)
	if !strings.Contains(out, "seqconnect=refused EPROTOTYPE") || !strings.Contains(out, "unix=connected") {
		t.Fatalf("rig: uncontained, a packet-sequenced socket must be refused the stream link by its type, and a stream socket must connect:\n%s", out)
	}
	if accepted() != 1 {
		t.Fatal("rig: the uncontained stream connection was not accepted")
	}
	argv, wall, err := pluginhost.ContainForTest(context.Background(), asks, places)
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	if wall.Filter, err = pluginhost.SyscallFilterForTest(true); err != nil {
		t.Fatal(err)
	}
	out = pluginhost.RunContainedForTest(t, argv, wall)
	if !strings.Contains(out, "seqconnect=refused") || !strings.Contains(out, "unix=refused") {
		t.Errorf("under a CUDA child's filter the link must stay unreachable:\n%s", out)
	}
	if n := accepted(); n != 1 {
		t.Errorf("the link accepted %d connections; only the uncontained probe's is expected", n)
	}
	t.Logf("under a CUDA child's filter: %s", strings.Join(strings.Fields(out), " "))
}
