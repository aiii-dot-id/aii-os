//go:build !android && !ios

package app

// The desktop facility set: exactly what this build honestly provides
// — transport + operator presence — and nothing that lacks a backend.
// The empty answer for audio/keystore is load-bearing: an advertised
// facility is a selection input, so a lie here becomes a variant
// activated against a service that does not exist.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestDesktopFacilitySet(t *testing.T) {
	set := hostFacilities()
	for _, want := range []string{pluginhost.FacilityTransportLocal, pluginhost.FacilityOperatorPresenceFresh} {
		if !slices.Contains(set, want) {
			t.Fatalf("desktop must advertise %s; set: %v", want, set)
		}
	}
	for _, absent := range []string{
		pluginhost.FacilityForegroundLifecycle, // no app lifecycle on a desktop daemon
		"sev_audio.raw_pcm",                    // no backend yet — the honest empty answer
		"sev_keystore.secret",                  // no backend yet
	} {
		if slices.Contains(set, absent) {
			t.Fatalf("desktop must NOT advertise %s (no implementation exists)", absent)
		}
	}
}

func TestResolveWorkerBinaryHonorsOperatorIntent(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "my-worker")

	// Operator names a missing path: refused loudly, never a silent
	// in-process downgrade.
	a := New(&Config{Plugins: PluginsConfig{WorkerBinary: named}})
	if _, _, err := a.resolveWorkerBinary(); err == nil {
		t.Fatal("a named-but-missing worker binary must refuse")
	}

	// The named path exists: resolved verbatim.
	if err := os.WriteFile(named, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, args, err := a.resolveWorkerBinary()
	if err != nil || got != named || len(args) != 0 {
		t.Fatalf("resolve = %q %q %v, want the named path alone", got, args, err)
	}
}

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
