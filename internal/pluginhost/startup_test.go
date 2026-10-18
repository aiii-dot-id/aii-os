package pluginhost

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func msPtr(n int64) *int64 { return &n }

// WHAT A BUILD MAY DECLARE ABOUT ITSELF, and what the host does with a
// declaration it cannot read.
func TestAProfileDeclaresAReservationAndAnAllowance(t *testing.T) {
	parse := func(extra string) (AcceleratorProfile, error) {
		raw := []byte(`{"v1":{"os":"linux","arch":"x86_64","backend":"cpu","precision":"fp16","models":["m"],` +
			`"memory_bytes":8589934592,"session_limit":2,"fallback":"none"` + extra + `}}`)
		got, err := ParseAccelerators(raw, []string{"v1"})
		return got["v1"], err
	}

	// Omitted is UNKNOWN, never zero: a host that must bound device
	// residency has to be able to tell "nothing declared" from "nothing
	// allocated".
	p, err := parse("")
	if err != nil {
		t.Fatalf("a profile without the optional declarations must parse: %v", err)
	}
	if p.DeviceMemoryBytes != nil || p.StartupMS != nil {
		t.Fatalf("undeclared must stay undeclared: device=%v startup=%v", p.DeviceMemoryBytes, p.StartupMS)
	}

	// Explicit zero is a claim of its own — this profile allocates no
	// device memory — and is not the same as saying nothing.
	if p, err = parse(`,"device_memory_bytes":0`); err != nil || p.DeviceMemoryBytes == nil || *p.DeviceMemoryBytes != 0 {
		t.Fatalf("an explicit zero device reservation must be kept: %v %v", p.DeviceMemoryBytes, err)
	}

	if p, err = parse(`,"startup_ms":180000,"device_memory_bytes":4294967296`); err != nil {
		t.Fatalf("a declared allowance and device reservation must parse: %v", err)
	}
	if p.StartupMS == nil || *p.StartupMS != 180000 || p.DeviceMemoryBytes == nil || *p.DeviceMemoryBytes != 4294967296 {
		t.Fatalf("the declarations were not kept: %+v", p)
	}
	if p.MemoryBytes != 8589934592 {
		t.Fatalf("the host-memory reservation was not kept: %d", p.MemoryBytes)
	}

	for _, tc := range []struct{ name, extra string }{
		{"an allowance of zero", `,"startup_ms":0`},
		{"a negative allowance", `,"startup_ms":-1`},
		{"an allowance beyond the bound", `,"startup_ms":3600001`},
		{"a negative device reservation", `,"device_memory_bytes":-1`},
		{"a reservation that is not a number", `,"device_memory_bytes":"lots"`},
	} {
		if _, err := parse(tc.extra); err == nil {
			t.Fatalf("%s was accepted", tc.name)
		}
	}
}

// WHOSE NUMBER, AND BOUNDED BY WHOSE. The operator's setting outranks
// what a package declares; a package's declaration outranks the default;
// and the operator's ceiling bounds every one of them, saying so.
func TestTheReadinessAllowanceHasAnOwnerAndACeiling(t *testing.T) {
	const id = "id.example.engine"
	declared := &AcceleratorProfile{StartupMS: msPtr(180000)}
	operator := func(d time.Duration) map[string]time.Duration { return map[string]time.Duration{id: d} }

	for _, tc := range []struct {
		name    string
		opts    *Options
		profile *AcceleratorProfile
		want    time.Duration
		source  string
		capped  bool
	}{
		{"nothing declared takes the host's default", &Options{}, nil, supervisor.DefaultReadyTimeout, "default", false},
		{"a package with no allowance takes it too", &Options{}, &AcceleratorProfile{}, supervisor.DefaultReadyTimeout, "default", false},
		{"what the package declares", &Options{}, declared, 180 * time.Second, "package", false},
		{"the operator outranks the package", &Options{ReadyTimeout: operator(40 * time.Second)}, declared, 40 * time.Second, "operator", false},
		{"the operator may give less than it asks", &Options{ReadyTimeout: operator(5 * time.Second)}, declared, 5 * time.Second, "operator", false},
		{"the ceiling bounds the package", &Options{StartupCeiling: 60 * time.Second}, declared, 60 * time.Second, "package", true},
		{"the ceiling bounds the operator too", &Options{StartupCeiling: 10 * time.Second, ReadyTimeout: operator(40 * time.Second)}, declared, 10 * time.Second, "operator", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := startupAllowance(tc.opts, id, tc.profile)
			if got.Effective != tc.want || got.Source != tc.source || got.Capped != tc.capped {
				t.Fatalf("allowance = %v from %q (capped %v), want %v from %q (capped %v)",
					got.Effective, got.Source, got.Capped, tc.want, tc.source, tc.capped)
			}
			if got.Ceiling <= 0 {
				t.Fatal("the readback must always name the ceiling that bounded it")
			}
		})
	}

	// The readback keeps what was ASKED for beside what was given, so a
	// capped allowance can be seen to be capped rather than guessed at.
	capped := startupAllowance(&Options{StartupCeiling: 60 * time.Second}, id, declared)
	if capped.Requested != 180*time.Second || capped.Effective != 60*time.Second || !capped.Capped {
		t.Fatalf("a capped allowance must keep the request: %+v", capped)
	}
	// An allowance nobody bounded takes the host's own ceiling.
	if a := startupAllowance(&Options{}, id, &AcceleratorProfile{StartupMS: msPtr(3600000)}); a.Ceiling != DefaultStartupCeiling || a.Effective != DefaultStartupCeiling || !a.Capped {
		t.Fatalf("the default ceiling must bound an unbounded ask: %+v", a)
	}
	// A plugin the operator named no deadline for is not charged another
	// plugin's, and nil options are not a panic.
	if a := startupAllowance(&Options{ReadyTimeout: operator(40 * time.Second)}, "id.example.other", nil); a.Source != "default" {
		t.Fatalf("one plugin's deadline reached another: %+v", a)
	}
	if a := startupAllowance(nil, id, nil); a.Effective != supervisor.DefaultReadyTimeout || a.Source != "default" {
		t.Fatalf("nil options must resolve to the default: %+v", a)
	}
}

// A setting may say which half of Speech it belongs beside — and nothing
// else: an unknown scope is a declaration the host does not honor.
func TestASettingMaySayWhichHalfOfSpeechItBelongsBeside(t *testing.T) {
	parse := func(scope string) ([]SettingDecl, error) {
		extra := ""
		if scope != "" {
			extra = `,"scope":"` + scope + `"`
		}
		return ParseSettings([]byte(`[{"key":"turn_pause_ms","type":"integer","title":"Pause"` + extra + `}]`))
	}
	for _, scope := range []string{"", ScopeHearing, ScopeSpeaking, ScopeSession} {
		got, err := parse(scope)
		if err != nil {
			t.Fatalf("scope %q must be honored: %v", scope, err)
		}
		if got[0].Scope != scope {
			t.Fatalf("scope %q was not kept: %q", scope, got[0].Scope)
		}
	}
	for _, scope := range []string{"louder", "Hearing", "hearing "} {
		if _, err := parse(scope); err == nil {
			t.Fatalf("scope %q was accepted", scope)
		}
	}
}

// A DECLARED ALLOWANCE REACHES THE CHILD. The operator configures
// nothing here, so the deadline this supervisor holds the child to is
// the one the build declared — and the refusal names it. Without this,
// a build could declare its allowance, the host could resolve it, and
// the child could still be killed at the default.
func TestADeclaredStartupAllowanceReachesTheSupervisor(t *testing.T) {
	skipWhereTheSandboxCannotBeEstablished(t)
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.declared-deadline"
	v := packagefmt.Variant{VariantID: "native", Entrypoint: "variants/native/child" + exeSuffix}
	res := &packagefmt.Result{Tier: packagefmt.TierT3, Manifest: &packagefmt.Manifest{ID: id},
		FileDigests: map[string]string{v.Entrypoint: digestOf(raw)}}
	profile := &AcceleratorProfile{Backend: "cpu", StartupMS: msPtr(30)}

	started := time.Now()
	sup, dir, _, err := startSupervisedNativeWith(context.Background(), res, &v, raw, nil, &Options{}, profile, startupAllowance(&Options{}, id, profile), "", nil)
	if sup != nil {
		defer sup.Close()
	}
	defer os.RemoveAll(dir)
	var exit *supervisor.ChildExitError
	if !errors.As(err, &exit) || exit.Phase != "start" || !strings.Contains(exit.Meaning, "30ms") {
		t.Fatalf("the build's declared allowance must be the deadline the child is held to: %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the declared allowance was not enforced")
	}

	// And the operator's own number outranks it here too, not only in
	// the resolver.
	operator := &Options{ReadyTimeout: map[string]time.Duration{id: 25 * time.Millisecond}}
	sup2, dir2, _, err := startSupervisedNativeWith(context.Background(), res, &v, raw, nil,
		operator, profile, startupAllowance(operator, id, profile), "", nil)
	if sup2 != nil {
		defer sup2.Close()
	}
	defer os.RemoveAll(dir2)
	if !errors.As(err, &exit) || !strings.Contains(exit.Meaning, "25ms") {
		t.Fatalf("the operator's deadline must outrank the build's at the supervisor: %v", err)
	}
}
