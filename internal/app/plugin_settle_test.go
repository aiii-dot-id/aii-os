package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
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
			if why := retryableRefusal(v); why != "" {
				log.Printf("test settle: %s refused (%s) — asking again, as Try again would: %s", v.ID, v.Refusal.Stage, why)
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
			case pluginfacility.StateActive:
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
				// .
				// .
				// .
				// .
				// .
				if !adoptedByApp(a, v.ID) {
					busy = true
				}
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
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if why := refusedNow(a); why != "" {
			t.Fatalf("the facility settled with refusals: %s", why)
		}
		return
	}
	var stuck []string
	for _, v := range a.facility.Snapshot().Instances {
		d := v.ID + "=" + string(v.State)
		if v.State == pluginfacility.StateActive && !adoptedByApp(a, v.ID) {
			d += " (the facility calls it active; the application has not adopted it)"
		}
		stuck = append(stuck, d)
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

// .
// .
// .
// .
// .
// .
// .
func whyNotActive(a *App) string {
	if a == nil || a.facility == nil {
		return "no facility"
	}
	var parts []string
	for _, v := range a.facility.Snapshot().Instances {
		d := v.ID + "=" + string(v.State)
		if v.Refusal != nil {
			d += "(" + string(v.Refusal.Stage) + "/" + string(v.Refusal.Class) + ")"
			if v.Refusal.Cause != nil {
				d += ": " + v.Refusal.Cause.Error()
			}
		}
		parts = append(parts, d)
	}
	if len(parts) == 0 {
		return "the facility holds no instances at all"
	}
	return strings.Join(parts, "; ")
}

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
func retryableRefusal(v pluginfacility.InstanceView) string {
	if v.State != pluginfacility.StateRefused || v.Refusal == nil {
		return ""
	}
	if v.Refusal.Class == pluginfacility.ClassTransient {
		return "transient"
	}
	if v.Refusal.Stage == pluginfacility.StageCancelled && v.Refusal.Cause != nil {
		if errors.Is(v.Refusal.Cause, context.DeadlineExceeded) {
			return "cancelled by a deadline, which on a loaded box is the machine and not a verdict"
		}
	}
	return ""
}

// .
// .
// .
// .
func adoptedByApp(a *App, id string) bool {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	_, ok := a.activeMeta[id]
	return ok
}

// .
// .
// .
// .
// .
// .
// .
func refusedNow(a *App) string {
	if a == nil || a.facility == nil {
		return ""
	}
	var parts []string
	for _, v := range a.facility.Snapshot().Instances {
		switch v.State {
		case pluginfacility.StateActive:
			continue
		case pluginfacility.StateRefused, pluginfacility.StateSkipped, pluginfacility.StateRemoved:
		default:
			// .
			// .
			continue
		}
		d := v.ID + " is " + string(v.State)
		if v.Refusal != nil {
			d += " at " + string(v.Refusal.Stage) + " (" + string(v.Refusal.Class) + ")"
			if v.Refusal.Cause != nil {
				d += ": " + v.Refusal.Cause.Error()
			}
			if v.Refusal.Remedy != "" {
				d += " — " + v.Refusal.Remedy
			}
		}
		parts = append(parts, d)
	}
	return strings.Join(parts, "; ")
}
