package app

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/machine"
)

// THE MACHINE SERVICE'S DIRECTORY IS AMONG THE PLACES EVERY WALL MASKS AND
// THE IDENTITY'S OWN FLOOR KEEPS: the account's own (~/.aii/machine) by
// default, a development service's where the slot's config names one
// (machine.dir, relative to the slot).
func TestTheMachineServicesDirectoryIsAPlaceTheHostKeeps(t *testing.T) {
	if !machine.Supported {
		t.Skip("no machine service on this row")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home directory on Windows (os.UserHomeDir)
	t.Setenv("SUDO_USER", "")
	// The account's service lies under this test's home (TestMain points
	// every other test at a directory of the binary's own).
	saved := accountMachineDir
	accountMachineDir = func() (string, error) { return filepath.Join(home, ".aii", "machine"), nil }
	t.Cleanup(func() { accountMachineDir = saved })
	slot := filepath.Join(home, ".aii", "identity-0")
	if err := os.MkdirAll(filepath.Join(slot, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Tools.CWD = slot
	cfg.SourcePath = filepath.Join(slot, "config", "config.json")
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(slot, "data", "identity.sec"), LedgerPath: filepath.Join(slot, "data", "ledger.jsonl"), DBPath: filepath.Join(slot, "data", "aii.db")}
	find := func(c Config) *firewall.Rule {
		for _, o := range substrateObjects(c, slot) {
			if o.ID == "sub.machine" {
				return o
			}
		}
		return nil
	}
	o := find(*cfg)
	if o == nil || o.Path != realPath(filepath.Join(home, ".aii", "machine")) || !o.Enforced || o.Kind != firewall.KindSubstrate {
		t.Fatalf("the account's machine directory is not a kept place: %+v", o)
	}
	if !strings.Contains(o.Reason, "machine service") {
		t.Errorf("the place's reason does not say whose it is: %q", o.Reason)
	}
	dev := filepath.Join(t.TempDir(), "dev-machine")
	cfg.Machine.Dir = dev
	if o := find(*cfg); o == nil || o.Path != realPath(dev) {
		t.Fatalf("a development service's directory is not a kept place: %+v", o)
	}
	if got, err := machineDirOf(*cfg, slot); err != nil || got != dev {
		t.Fatalf("machine.dir absolute: %q, %v", got, err)
	}
	cfg.Machine.Dir = "machine-dev"
	if got, err := machineDirOf(*cfg, slot); err != nil || got != filepath.Join(slot, "machine-dev") {
		t.Fatalf("a relative machine.dir is the slot's: %q, %v", got, err)
	}
}

// THE PAGE'S LINE IS THE LINK AS IT STANDS, through a real service and
// this identity's real link: none where there is no link; linked, with the
// service's build and the count; and once the service stops, not running,
// with what the link met and a remedy.
func TestThePluginsPageLineIsTheLinkAsItStands(t *testing.T) {
	a := &App{cfg: defaultConfig()}
	if a.machineView() != nil {
		t.Fatal("an identity with no link shows no line")
	}
	if !machine.Supported {
		return
	}
	// A socket's address is short on macOS (104 bytes), so the link's
	// directory is under /tmp there; Windows has no /tmp, and a path
	// without its drive is not absolute.
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	root, err := os.MkdirTemp(base, "aiia")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if runtime.GOOS == "linux" {
		rt := filepath.Join(root, "run")
		if err := os.Mkdir(rt, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", rt)
	}
	dir := filepath.Join(root, "m")
	slot := filepath.Join(root, "identity-0")
	if err := os.Mkdir(slot, 0o700); err != nil {
		t.Fatal(err)
	}
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	srvCtx, stopSrv := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- machine.NewServer(machine.Options{Dir: dir, Build: machine.Build{Version: "9.9.9", Commit: "service00000"},
			StartupSlots: func() ([]string, error) { return []string{slot}, nil }}).Serve(srvCtx)
	}()
	t.Cleanup(func() { stopSrv(); <-served })
	cfg := defaultConfig()
	cfg.Machine.Dir = dir
	a = &App{cfg: cfg}
	c := machine.NewClient(machine.ClientOptions{Dir: dir, Slot: slot, Key: kp, PollEvery: 50 * time.Millisecond})
	a.machine.client.Store(c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	view := func(want string) *dashboard.MachineView {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if v := a.machineView(); v != nil && v.State == want {
				return v
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("the line never said %q: %+v", want, a.machineView())
		return nil
	}

	v := view(machine.StateLinked)
	for deadline := time.Now().Add(10 * time.Second); v.Linked != 1 && time.Now().Before(deadline); v = view(machine.StateLinked) {
		time.Sleep(20 * time.Millisecond)
	}
	if v.Build != "9.9.9, service00000" || v.Linked != 1 || !v.CanForce || v.Update != "" {
		t.Fatalf("the linked line: %+v", v)
	}
	stopSrv()
	<-served
	served <- nil
	v = view(machine.StateDown)
	if v.Reason == "" || !strings.Contains(v.Remedy, "aii machine -dir "+dir) {
		t.Fatalf("a stopped development service's line says what it met and how to start it: %+v", v)
	}
}

// THE CONFIG NAMES A DEVELOPMENT SERVICE'S DIRECTORY, and only that: the
// field round-trips through the one parser, and an absent field is the
// account's own service.
func TestMachineDirIsReadByTheOneParser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"machine": {"dir": "dev-machine"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Machine.Dir != "dev-machine" {
		t.Fatalf("machine.dir = %q", cfg.Machine.Dir)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = ReadConfig(path); err != nil || cfg.Machine.Dir != "" {
		t.Fatalf("absent machine.dir: %q, %v", cfg.Machine.Dir, err)
	}
}

func TestNoTestReachesTheAccountsMachineService(t *testing.T) {
	real, err := install.MachineDir()
	if err != nil {
		t.Skipf("this account has no home to find a machine directory in: %v", err)
	}
	got, err := machineDirOf(*defaultConfig(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got == real {
		t.Fatalf("a test's default configuration links to the account's own machine service at %s", real)
	}
	if accountRelaunch() != nil {
		t.Fatal("a test boot would start the account's machine service")
	}
}
