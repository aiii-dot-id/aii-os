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
		facility.ForegroundLifecycle, // no app lifecycle on a desktop daemon
		"sev_audio.raw_pcm",          // no backend yet — the honest empty answer
		"sev_keystore.secret",        // no backend yet
	} {
		if set.Has(absent) {
			t.Fatalf("desktop must NOT advertise %s (no implementation exists)", absent)
		}
	}

	// Presence is a live status on a real seam, not a constant: before
	// any dashboard exists it reads not-present; membership stays.
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

	// Operator names a missing path: refused loudly, never a silent
	// in-process downgrade.
	a := New(&Config{Plugins: PluginsConfig{WorkerBinary: named}})
	if _, err := a.resolveWorkerBinary(); err == nil {
		t.Fatal("a named-but-missing worker binary must refuse")
	}

	// The named path exists: resolved verbatim.
	if err := os.WriteFile(named, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := a.resolveWorkerBinary()
	if err != nil || got != named {
		t.Fatalf("resolve = %q %v, want the named path", got, err)
	}
}
