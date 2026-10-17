//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// THE SELECTION'S ALLOWANCE IS THE NATIVE CHILD'S DEADLINE, through the
// whole activation. Stage resolves the readiness allowance once, from
// the operator's options and the selected accelerator profile
// (sel.Startup); Start records that value on the activation and hands
// the same value to the supervisor. The child here never reports ready,
// so the refusal names the allowance that ended it — the package's own
// startup_ms, or the operator's number over it.
func TestTheSelectionsAllowanceIsTheNativeChildsDeadline(t *testing.T) {
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	fx := newModelFixture(t, "model.bin", 4096)
	models, err := json.Marshal([]ModelDecl{fx.decl})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		startupMS int64
		operator  time.Duration
		want      string
	}{
		{"the package's startup_ms", 30, 0, "within 30ms (the package asked for 30ms;"},
		{"the operator's over the package's", 600000, 25 * time.Millisecond, "within 25ms (you set 25ms;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "org.example.allowance-" + map[bool]string{true: "operator", false: "package"}[tc.operator > 0]
			profile := AcceleratorProfile{OS: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Backend: "cpu", Precision: "int8",
				Models: []string{fx.decl.Name}, MemoryBytes: 1 << 20, SessionLimit: 1, Fallback: "none", StartupMS: msPtr(tc.startupMS)}
			accel, err := json.Marshal(map[string]AcceleratorProfile{hostVariantID(): profile})
			if err != nil {
				t.Fatal(err)
			}
			pkg := nativePackageWithFiles(t, id, child, role, map[string][]byte{ModelsFile: models, AcceleratorFile: accel})
			opts := supervisedOpts(Options{Roots: roots})
			useStore(t, opts, fx.fetch)
			if tc.operator > 0 {
				opts.ReadyTimeout = map[string]time.Duration{id: tc.operator}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			started := time.Now()
			ap, err := Activate(ctx, pkg, newRegistry(t), opts)
			if ap != nil {
				_ = ap.Deactivate(context.Background())
				t.Fatal("a child that never reports ready was activated")
			}
			var exit *supervisor.ChildExitError
			if !errors.As(err, &exit) || exit.Phase != "start" || !strings.Contains(exit.Meaning, tc.want) {
				t.Fatalf("the refusal must name the selection's allowance (%q): %v", tc.want, err)
			}
			if took := time.Since(started); took > 20*time.Second {
				t.Fatalf("the allowance was not the deadline: %s", took)
			}
		})
	}
}

// The WASM lane holds its worker to the same value. Start bounds the
// whole WASM start with the allowance as well (its context), so the
// lane is driven here on its own, under a context with no deadline, by
// a worker stand-in that never reports ready: what ends it is the
// supervisor's readiness deadline, and that is the selection's.
func TestTheWASMLaneHoldsItsWorkerToTheSelectionsAllowance(t *testing.T) {
	const id = "org.example.allowance-wasm"
	account := []byte(`[{"id":"ping","summary":"answers"}]`)
	pkg := describingPkg(t, id, account, account)
	opts := &Options{WorkerBinary: fakechildBin, WorkerArgs: []string{"session"}, ReadyTimeout: map[string]time.Duration{id: 25 * time.Millisecond}}
	st, err := Stage(context.Background(), pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	ap := &ActivePlugin{ID: id}
	inv, err := st.startWASMSupervised(context.Background(), ap, nil, "")
	if inv != nil {
		_ = ap.closeChannel(context.Background())
		t.Fatal("a worker that never reports ready was started")
	}
	var exit *supervisor.ChildExitError
	if !errors.As(err, &exit) || exit.Phase != "start" || !strings.Contains(exit.Meaning, "within 25ms (you set 25ms;") {
		t.Fatalf("the refusal must name the selection's allowance: %v", err)
	}
	if ap.Startup == nil || ap.Startup.Effective != 25*time.Millisecond {
		t.Fatalf("the activation records the allowance it handed the supervisor: %+v", ap.Startup)
	}
}

// The worker is handed the same allowance for its own bound. A proving
// WASM start passes the worker -startup-timeout, the bound it holds
// module admission and the descriptor proof to (zero would mean its
// invoke timeout instead); the supervisor's deadline sits on top of it,
// so a worker that ignored it would still be stopped and only the
// argument tells them apart. The stand-in reports its arguments on its
// readiness line — the proof mark the lane waits for.
func TestTheWASMWorkerIsHandedTheSelectionsAllowance(t *testing.T) {
	const id = "org.example.allowance-worker-args"
	account := []byte(`[{"id":"ping","summary":"answers"}]`)
	pkg := describingPkg(t, id, account, account)
	opts := &Options{WorkerBinary: fakechildBin, WorkerArgs: []string{"worker-args"}, ReadyTimeout: map[string]time.Duration{id: 7 * time.Second}}
	st, err := Stage(context.Background(), pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	ap := &ActivePlugin{ID: id}
	if _, err := st.startWASMSupervised(context.Background(), ap, nil, "proof-of-the-account"); err != nil {
		t.Fatalf("the stand-in reports the proof mark and is ready: %v", err)
	}
	defer ap.closeChannel(context.Background())
	line := ap.sup.ReadyLine()
	for _, want := range []string{"-descriptor-proof=proof-of-the-account", "-startup-timeout=7s"} {
		if !strings.Contains(" "+line+" ", " "+want+" ") {
			t.Fatalf("the worker's arguments carry %s: %q", want, line)
		}
	}
}
