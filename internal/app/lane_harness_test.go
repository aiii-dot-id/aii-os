package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
)

// The test binary is not aii, so the package's tests stand in for the
// processes aii would start of itself. The worker lane is one of them, on
// the rows that have one (lane_harness_desktop_test.go).
func TestMain(m *testing.M) {
	// Nor is it the Vulkan probe: given that subcommand it would run this
	// whole suite in a child the query's deadline then kills. The
	// measurement's tests point the probe at a fixture of their own.
	vulkanProbe = func() (string, []string, error) { return "", nil, errors.New("a test binary is not the Vulkan probe") }
	// Capability lookups are offline by default in tests. Tests that need
	// an answer configure their fixture URL and admit it through this guard.
	modalityGuard = func(context.Context, string) error { return firewall.ErrEgressBlocked }

	root, owner := os.Getenv(machineRootEnv), false
	if root == "" {
		var err error
		if root, err = os.MkdirTemp("", "aii-app-test-machine"); err != nil {
			fmt.Fprintln(os.Stderr, "the test binary's machine directory:", err)
			os.Exit(1)
		}
		owner = true
		os.Setenv(machineRootEnv, root)
	}
	machineDir, err := os.MkdirTemp(root, "machine")
	if err != nil {
		fmt.Fprintln(os.Stderr, "the test binary's machine directory:", err)
		os.Exit(1)
	}
	accountMachineDir = func() (string, error) { return machineDir, nil }
	accountRelaunch = func() func() error { return nil }

	publicname.PrepareOffByDefaultForTest()
	asked := watchPlatformDials()
	code := m.Run()
	if owner {
		os.RemoveAll(root)
	} else {
		os.RemoveAll(machineDir)
	}
	if n, first := asked(); n > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: this test binary opened %d connection(s) to the platform's own servers, the first to %s. A test that boots an app names genesistest.Ring5Server as its firewall_url (safebootConfig does); only the test seam asks the platform.\n", n, first)
		code = 1
	}
	os.Exit(code)
}

// machineRootEnv names, to a child this binary runs of itself, the directory
// its machine directory is made in: its parent's, removed with it.
const machineRootEnv = "AII_APP_TEST_MACHINE_ROOT"

// platformServers are the servers a boot or a birth asks.
var platformServers = map[string]bool{"genesis.aiii.id": true, "firewall.aiii.id": true, "bootstrap.aiii.id": true}

// watchPlatformDials counts the connections this test binary opens to the
// platform's servers through the default transport, which is what the
// genesis client dials with. The transport stays the *http.Transport it
// is (production code clones it); only its dial is wrapped. The test
// seam's own round is not seen: it dials through a copy made before this
// runs.
func watchPlatformDials() func() (int64, string) {
	var n atomic.Int64
	var first atomic.Value
	tr := http.DefaultTransport.(*http.Transport)
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil && platformServers[host] {
			if n.Add(1) == 1 {
				first.Store(addr)
			}
		}
		return dial(ctx, network, addr)
	}
	return func() (int64, string) {
		s, _ := first.Load().(string)
		return n.Load(), s
	}
}
