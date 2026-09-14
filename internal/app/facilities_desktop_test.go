//go:build !android && !ios

package app

// .
// .
// .
// .
// .

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/facility"
)

func TestDesktopFacilitySet(t *testing.T) {
	a := New(&Config{})
	set, err := a.hostFacilities()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{facility.TransportLocal, facility.OperatorPresenceFresh} {
		if !set.Has(want) {
			t.Fatalf("desktop must advertise %s; set: %v", want, set.Names())
		}
	}
	for _, absent := range []string{
		facility.ForegroundLifecycle,
		"sev_audio.raw_pcm",
		"sev_keystore.secret",
	} {
		if set.Has(absent) {
			t.Fatalf("desktop must NOT advertise %s (no implementation exists)", absent)
		}
	}

	// .
	// .
	for _, row := range set.Snapshot() {
		if row.Name == facility.OperatorPresenceFresh && row.Live {
			t.Fatal("presence must read not-present before any session source exists")
		}
		if row.Provider == "" {
			t.Fatalf("every advertised facility carries provider info, %s does not", row.Name)
		}
	}
}

func TestResolveWorkerBinaryHonorsOperatorIntent(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "my-worker")

	// .
	// .
	a := New(&Config{Plugins: PluginsConfig{WorkerBinary: named}})
	if _, _, err := a.resolveWorkerBinary(); err == nil {
		t.Fatal("a named-but-missing worker binary must refuse")
	}

	// .
	if err := os.WriteFile(named, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, args, err := a.resolveWorkerBinary()
	if err != nil || got != named || len(args) != 0 {
		t.Fatalf("resolve = %q %q %v, want the named path alone", got, args, err)
	}
}

// .
// .
// .
// .
func TestTheSupervisedLaneIsTheDaemonReExecuted(t *testing.T) {
	bin, args, err := defaultWorkerLane()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if bin != exe {
		t.Fatalf("worker binary = %q, want this executable %q", bin, exe)
	}
	if len(args) != 1 || args[0] != WorkerSubcommand {
		t.Fatalf("worker args = %q, want [%q]", args, WorkerSubcommand)
	}
}
