package app

import (
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
func settlePlugins(a *App, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if a.facility == nil {
			return true
		}
		busy := false
		for _, v := range a.facility.Snapshot().Instances {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			if v.State == pluginfacility.StateRefused && v.Refusal != nil && v.Refusal.Class == pluginfacility.ClassTransient {
				log.Printf("test settle: %s refused transiently (%s) — asking again, as Try again would", v.ID, v.Refusal.Stage)
				a.facility.Retry(v.ID)
				busy = true
				continue
			}
			switch v.State {
			case pluginfacility.StateAcquiring, pluginfacility.StateAdmitting,
				pluginfacility.StateStarting, pluginfacility.StateUpdating,
				pluginfacility.StateWanted, pluginfacility.StateDiscovered,
				// .
				// .
				// .
				// .
				// .
				// .
				// .
				pluginfacility.StateDraining:
				busy = true
			}
		}
		if !busy {
			a.applyFacilitySnapshot()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// .
// .
// .
func awaitPlugins(t *testing.T, a *App) {
	t.Helper()
	if settlePlugins(a, 30*time.Second) {
		return
	}
	var stuck []string
	for _, v := range a.facility.Snapshot().Instances {
		stuck = append(stuck, v.ID+"="+string(v.State))
	}
	t.Fatalf("the facility never settled: %v", stuck)
}

// .
// .
// .
func awaitPluginVersion(t *testing.T, a *App, id, version string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if v, _ := activeRelease(a, id); v == version {
			a.applyFacilitySnapshot()
			return
		}
		if time.Now().After(deadline) {
			v, n := activeRelease(a, id)
			var states []string
			if a.facility != nil {
				for _, iv := range a.facility.Snapshot().Instances {
					states = append(states, fmt.Sprintf("%s=%s v=%s gens=%v %s",
						iv.ID, iv.State, iv.Version, iv.Generations, refusalReason(iv.Refusal)))
				}
			}
			t.Fatalf("%s never reached %s: serving %q, %d active; facility: %v", id, version, v, n, states)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
