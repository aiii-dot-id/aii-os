package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// THE OPERATOR'S ADMISSION KNOBS LOAD, REFUSE NONSENSE, AND REACH THE
// POLICY. A reserve, a budget and a concurrency cap are named or
// omitted, never negative; what is named is what the facility admits
// against.
func TestAdmissionKnobsLoadAndReachThePolicy(t *testing.T) {
	for _, tc := range []struct {
		runtime string
		bad     bool
	}{
		{`{}`, false},
		{`{"admission_memory_reserve_bytes":1073741824,"admission_memory_budget_bytes":8589934592,"max_concurrent_starts":2}`, false},
		{`{"admission_memory_reserve_bytes":-1}`, true},
		{`{"admission_memory_budget_bytes":-5}`, true},
		{`{"max_concurrent_starts":-2}`, true},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":`+tc.runtime+`}}`), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if tc.bad {
			if err == nil {
				t.Fatalf("a negative admission knob loaded: %s", tc.runtime)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.runtime, err)
		}
		a := &App{cfg: cfg}
		pol := a.pluginPolicy(*cfg, 0, false)
		want := pluginfacility.AdmissionPolicy{ReserveBytes: cfg.Plugins.Runtime.AdmissionMemoryReserveBytes,
			BudgetBytes: cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes, MaxConcurrentStarts: cfg.Plugins.Runtime.MaxConcurrentStarts}
		if pol.Admission != want {
			t.Fatalf("the policy carries the knobs: %+v, want %+v", pol.Admission, want)
		}
	}
	// A start waiting for room says so on its card, in the facility's
	// own sentence, where a bare "starting" used to stand.
	l := pluginLifecycle{phase: lifeStarting, note: "waiting for 3.0 GB of host memory: 1.0 GB free of 16.0 GB, 12.0 GB reserved by other engines"}
	if got := lifecycleText(l); !strings.Contains(got, "waiting for 3.0 GB") {
		t.Fatalf("the admission sentence reaches the card: %q", got)
	}
}

// THE PLATFORM MEASURES HONESTLY: on this machine the measurer either
// reports a total with an available figure below it, or says it does
// not know — never a figure it invented.
func TestHostCapacityMeasuresOrSaysItCannot(t *testing.T) {
	av := hostCapacity{}.Measure()
	if av.HostKnown && (av.HostTotal <= 0 || av.HostAvailable <= 0 || av.HostAvailable > av.HostTotal) {
		t.Fatalf("a known measurement must be coherent: %+v", av)
	}
	t.Logf("host capacity: known=%v total=%d available=%d", av.HostKnown, av.HostTotal, av.HostAvailable)
}
