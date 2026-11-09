package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	configdir "github.com/aiii-dot-id/aii-os/config"
	"github.com/aiii-dot-id/aii-os/internal/agentmemory"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The test binary is not aii, so the package's tests stand in for the
// processes aii would start of itself. The worker lane is one of them, on
// the rows that have one (lane_harness_desktop_test.go).
func TestMain(m *testing.M) {
	// NO TEST REACHES THE LIVE WITNESS. The shipped config names the hosted one, so a test that
	// boots on defaultConfig asks it at its first witness pass, thirty seconds in, if it lasts
	// that long. A test that needs a witness puts a witnesstest one in its config.
	configdir.Config = withoutHostedWitness(configdir.Config)
	// Nor is it the Vulkan probe: given that subcommand it would run this
	// whole suite in a child the query's deadline then kills. The
	// measurement's tests point the probe at a fixture of their own.
	vulkanProbe = func() (string, []string, error) { return "", nil, errors.New("a test binary is not the Vulkan probe") }
	// Capability lookups are offline by default in tests. Tests that need
	// an answer configure their fixture URL and admit it through this guard.
	modalityGuard = func(context.Context, string) error { return firewall.ErrEgressBlocked }
	selectedModelDiscovery = func(*App, context.Context, providerEntry, string) ([]string, map[string]modelMeta, error) {
		return nil, nil, errors.New("native capability discovery is offline in test boot fixtures")
	}

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
	otherAgentStores = func() []agentmemory.Store { return nil }
	asked := watchPlatformDials()
	code := m.Run()
	if owner {
		os.RemoveAll(root)
	} else {
		os.RemoveAll(machineDir)
	}
	// The binary fails for a dial, and a test's own failure keeps its exit. What
	// the first dial saw was said when it happened, so a failing test or a
	// package's timeout cannot hide it.
	if n, _ := asked(); n > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: this test binary asked for %d connection(s) to the platform's own servers or the hosted witness, refused here. A test that boots an app names genesistest.Ring5Server as its firewall_url and is on no witness or a witnesstest one (safebootConfig does); only the test seam asks the platform, and no test asks the hosted witness. The first was said above when it happened.\n", n)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// withoutHostedWitness is the shipped config on no witness. A shipped config
// that no longer reads as this expects stops the binary, rather than leave its
// tests free to dial.
func withoutHostedWitness(shipped []byte) []byte {
	hosted := []byte(`"url": "https://witness.aiii.id"`)
	if bytes.Count(shipped, hosted) != 1 {
		panic("the shipped config no longer names the hosted witness as this expects")
	}
	return bytes.Replace(shipped, hosted, []byte(`"url": ""`), 1)
}

// A test's defaultConfig is on no witness, and the file that ships still names the hosted one.
func TestADefaultConfigInATestIsOnNoWitness(t *testing.T) {
	if got := defaultConfig().Witness.URL; got != "" {
		t.Errorf("a test's defaultConfig is on the witness %q", got)
	}
	shipped, err := ReadConfig(filepath.Join("..", "..", "config", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if shipped.Witness.URL != "https://witness.aiii.id" {
		t.Errorf("the shipped file names %q", shipped.Witness.URL)
	}
}

// machineRootEnv names, to a child this binary runs of itself, the directory
// its machine directory is made in: its parent's, removed with it.
const machineRootEnv = "AII_APP_TEST_MACHINE_ROOT"

// platformServers are the servers a boot or a birth asks, and the hosted
// witness the default configuration names: no test reaches it either
// (safebootConfig is on no witness; a test that needs one uses witnesstest).
var platformServers = map[string]bool{"genesis.aiii.id": true, "firewall.aiii.id": true, "bootstrap.aiii.id": true, "witness.aiii.id": true}

// watchPlatformDials refuses and counts the connections this test binary asks of
// the platform's servers through the default transport, which is what the
// genesis client dials with. The transport stays the *http.Transport it
// is (production code clones it); only its dial is wrapped. The test
// seam's own round is not seen: it dials through a copy made before this
// runs.
func watchPlatformDials() func() (int64, string) {
	return watchDials(http.DefaultTransport.(*http.Transport), platformServers, func(first string) {
		fmt.Fprintln(os.Stderr, "A REFUSED DIAL: "+first)
	})
}

// errPlatformDial is what a test binary's dial to a platform server gets: it
// is counted, and it does not leave the machine.
var errPlatformDial = errors.New("this test binary does not dial the platform's servers")

// watchDials refuses a transport's dials to the servers named, counts them, and
// says what the first one saw as it happens.
func watchDials(tr *http.Transport, servers map[string]bool, say func(first string)) func() (int64, string) {
	var mu sync.Mutex
	var n int64
	var first string
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil && servers[host] {
			mu.Lock()
			defer mu.Unlock()
			if n++; n == 1 {
				first = dialSeen(addr)
				say(first)
			}
			return nil, errPlatformDial
		}
		return dial(ctx, network, addr)
	}
	return func() (int64, string) {
		mu.Lock()
		defer mu.Unlock()
		return n, first
	}
}

var testOnAStack = regexp.MustCompile(`internal/app\.(Test[A-Za-z0-9_]+)`)

// dialSeen is the address, the tests on a stack at that moment, and every
// goroutine's stack. A transport dials on a goroutine of its own, whose stack
// names no caller, so one stack would not say who asked.
func dialSeen(addr string) string {
	buf := make([]byte, 16<<20)
	stacks := string(buf[:runtime.Stack(buf, true)])
	var tests []string
	for _, m := range testOnAStack.FindAllStringSubmatch(stacks, -1) {
		if m[1] != "TestMain" && !slices.Contains(tests, m[1]) {
			tests = append(tests, m[1])
		}
	}
	return fmt.Sprintf("%s, with these tests on a stack: %s. Every goroutine at that dial:\n%s", addr, strings.Join(tests, ", "), stacks)
}

// THE GUARD REFUSES WHAT DIALLED, AND NAMES IT. A dial to a server it watches does not leave the
// machine, and it says the address, the test running and the goroutine that asked, or a failure at
// the end of a long binary points at nothing.
func TestThePlatformGuardRefusesAndNamesWhatDialled(t *testing.T) {
	var reached atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Add(1) }))
	defer srv.Close()
	host, _, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	var said string
	asked := watchDials(tr, map[string]bool{host: true}, func(first string) { said = first })
	if n, first := asked(); n != 0 || first != "" {
		t.Fatalf("before any dial the guard reads %d %q", n, first)
	}
	if _, err := (&http.Client{Transport: tr}).Get(srv.URL); !errors.Is(err, errPlatformDial) {
		t.Errorf("the dial was not refused: %v", err)
	}
	if got := reached.Load(); got != 0 {
		t.Errorf("the server was reached %d times", got)
	}
	n, first := asked()
	if n != 1 {
		t.Fatalf("the guard counted %d dials", n)
	}
	if said != first {
		t.Errorf("what the guard said as it happened is not what it kept: %q", said)
	}
	for _, want := range []string{srv.Listener.Addr().String(), "with these tests on a stack: TestThePlatformGuardRefusesAndNamesWhatDialled.", "net/http.(*Client).Get", "net/http.(*Transport).dial"} {
		if !strings.Contains(first, want) {
			t.Errorf("the guard does not say %q in:\n%s", want, first)
		}
	}
}

// A DIAL IS SAID WHEN IT HAPPENS, AND THE BINARY FAILS FOR IT, AND A TEST'S OWN FAILURE DOES NOT HIDE
// IT. Each case is this binary run again, on a local server only: a test that fails, one that
// dials, and both.
func TestARefusedDialIsSaidBesideAFailingTest(t *testing.T) {
	if mode := os.Getenv(dialChildEnv); mode != "" {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer srv.Close()
		host, _, _ := net.SplitHostPort(srv.Listener.Addr().String())
		if strings.Contains(mode, "dial") {
			platformServers[host] = true
			if _, err := http.Get(srv.URL); !errors.Is(err, errPlatformDial) {
				t.Errorf("the dial was not refused: %v", err)
			}
		}
		if strings.Contains(mode, "fail") {
			t.Error("this test fails on its own")
		}
		return
	}
	for _, c := range []struct {
		mode        string
		wantDial    bool
		wantOwnFail bool
	}{{"dial", true, false}, {"fail", false, true}, {"dial and fail", true, true}} {
		cmd := exec.Command(os.Args[0], "-test.run", "^TestARefusedDialIsSaidBesideAFailingTest$", "-test.count=1")
		cmd.Env = append(os.Environ(), dialChildEnv+"="+c.mode)
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Errorf("%s: the binary ended %v, want exit 1:\n%s", c.mode, err, out)
		}
		said := strings.Contains(string(out), "A REFUSED DIAL: ") && strings.Contains(string(out), "TestARefusedDialIsSaidBesideAFailingTest.")
		counted := strings.Contains(string(out), "FAIL: this test binary asked for 1 connection(s)")
		if said != c.wantDial || counted != c.wantDial {
			t.Errorf("%s: said=%v counted=%v, want %v:\n%s", c.mode, said, counted, c.wantDial, out)
		}
		if own := strings.Contains(string(out), "this test fails on its own"); own != c.wantOwnFail {
			t.Errorf("%s: its own failure shown=%v, want %v:\n%s", c.mode, own, c.wantOwnFail, out)
		}
	}
}

const dialChildEnv = "AII_APP_TEST_DIAL_CHILD"
