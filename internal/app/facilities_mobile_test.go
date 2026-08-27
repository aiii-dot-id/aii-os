//go:build android || ios

package app

// The mobile facility set: the shells add the foreground lifecycle
// (SetForeground exists) and refuse the exec lane (iOS forbids exec;
// bundled T3 is in-process — PLUGIN_FRAMEWORK §13). Runs only on a
// device/emulator suite; the five-platform sweep compiles it.

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
		facility.ForegroundLifecycle, // the mobile-only facility
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
	// Even a config naming a worker binary resolves to none: the OS
	// app sandbox is the wall and in-process is the only binding.
	a := New(&Config{Plugins: PluginsConfig{WorkerBinary: "/nonexistent/worker"}})
	got, err := a.resolveWorkerBinary()
	if err != nil || got != "" {
		t.Fatalf("mobile resolve = %q %v, want empty and no error", got, err)
	}
}
