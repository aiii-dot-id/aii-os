//go:build android || ios

package app

// .
// .
// .
// .

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/facility"
)

func TestMobileFacilitySet(t *testing.T) {
	a := New(&Config{})
	set, err := a.hostFacilities()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		facility.TransportLocal,
		facility.OperatorPresenceFresh,
		facility.ForegroundLifecycle,
	} {
		if !set.Has(want) {
			t.Fatalf("mobile must advertise %s; set: %v", want, set.Names())
		}
	}
	for _, absent := range []string{"sev_audio.raw_pcm", "sev_keystore.secret"} {
		if set.Has(absent) {
			t.Fatalf("mobile must NOT advertise %s (no backend exists)", absent)
		}
	}
}

func TestMobileHasNoSupervisedLane(t *testing.T) {
	// .
	// .
	a := New(&Config{Plugins: PluginsConfig{WorkerBinary: "/nonexistent/worker"}})
	got, args, err := a.resolveWorkerBinary()
	if err != nil || got != "" || args != nil {
		t.Fatalf("mobile resolve = %q %q %v, want empty and no error", got, args, err)
	}
}
