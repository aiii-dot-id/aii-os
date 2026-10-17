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

// BOOT NO LONGER WAITS FOR PLUGINS. The identity answers while they are
// still coming up, and they come up beside each other rather than one
// after another — which is the point of the facility and the reason a
// native engine no longer holds the whole identity for half a minute.
//
// A test that asserts on an activation therefore has to wait for it.
// These are that wait, in one place, so every test says what it means
// rather than each one growing its own polling loop.

// settlePlugins waits until the facility has nothing in flight: no
// attempt starting, acquiring, admitting or updating. It returns
// whether it settled, so a caller can decide whether that is a failure.
func settlePlugins(a *App, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if a.facility == nil {
			return true // nothing was ever handed over
		}
		busy := false
		for _, v := range a.facility.Snapshot().Instances {
			// A TRANSIENT REFUSAL IS NOT AN OUTCOME, it is a wait. The
			// facility will try again after its backoff, which is
			// thirty seconds and rightly so on a real host — far longer
			// than a test should sit. Under a loaded gate a wasm
			// activation can miss a readiness deadline and refuse
			// exactly this way, and a helper that called that "settled"
			// would hand the test an empty active set and a message
			// that did not say why. So this does what the operator's
			// Try again does, and says that it did.
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
				// DRAINING IS IN FLIGHT TOO. A predecessor whose custody
				// is not yet discharged holds back the next candidate,
				// so an id that looks idle is still on its way
				// somewhere. Leaving this out made the helper call a
				// half-finished reinstall settled — which only showed
				// up under -race, where the drain is slow enough to
				// still be running when the helper looked.
				pluginfacility.StateDraining:
				busy = true
			case pluginfacility.StateActive:

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

// awaitPlugins settles or fails the test, naming what was still in
// flight — a timeout here is a plugin that never finished, not a slow
// machine.
func awaitPlugins(t *testing.T, a *App) {
	t.Helper()
	if settlePlugins(a, 30*time.Second) {
		// SETTLED IS NOT ACTIVE. An instance that ended in a refusal
		// this helper does not retry is settled — the facility is done
		// with it — so a caller that then counts actives sees a bare
		// number and no reason. Three gate failures were unattributable
		// for exactly that: "all three must come up, got 0" said
		// nothing about which one refused, at which stage, or why. The
		// reason exists here; it is reported here.
		if why := refusedNow(a); why != "" {
			t.Fatalf("the facility settled with refusals: %s", why)
		}
		return
	}
	t.Fatalf("the facility never settled: %v", inFlight(a))
}

// inFlight names what keeps the facility from settling: each instance and
// its state, and the reason a refusal gives.
func inFlight(a *App) []string {
	if a.facility == nil {
		return nil
	}
	var stuck []string
	for _, v := range a.facility.Snapshot().Instances {
		d := v.ID + "=" + string(v.State)
		if v.State == pluginfacility.StateActive && !adoptedByApp(a, v.ID) {
			d += " (the facility calls it active; the application has not adopted it)"
		}
		if v.Refusal != nil {
			d += " " + refusalReason(v.Refusal)
		}
		stuck = append(stuck, d)
	}
	return stuck
}

// awaitPluginVersion waits for one id to be serving a particular
// release — what an update means, now that it lands behind the pass
// that asked for it rather than inside it.
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
						iv.ID, iv.State, iv.Version, len(iv.Activations), refusalReason(iv.Refusal)))
				}
			}
			t.Fatalf("%s never reached %s: serving %q, %d active; facility: %v", id, version, v, n, states)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// whyNotActive describes what the facility is holding, for a failure
// that would otherwise say only how many plugins were missing.
//
// A test that reports a count makes the reader guess; a test that
// reports "one=refused(cancelled/permanent): context deadline exceeded"
// hands them the answer. The count was what made three gate failures
// unattributable.
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

// retryableRefusal reports why an instance should be asked again, or ""
// when its refusal is a real answer the test should report.
//
// A transient refusal is the facility saying "not yet", and Try again
// is what an operator would press. A CANCELLED refusal is the harder
// case: the executor marks a stage Cancelled both when an instance is
// genuinely withdrawn AND when its own context expires, and those are
// not the same event wearing one name. Under a loaded gate the second
// happens because the machine is busy, which is a wait, not a verdict —
// so it is asked again, bounded by the caller's deadline like any other
// wait. A withdrawal has nothing to ask again for and is left alone.
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

func adoptedByApp(a *App, id string) bool {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	_, ok := a.activeMeta[id]
	return ok
}

// refusedNow names every instance that settled somewhere other than
// active, with whatever reason the facility holds.
//
// Not only refusals: skipped is policy keeping a package off, removed is
// a package gone, and both are settled answers that leave a caller
// counting zero. A test waiting for three activations deserves to be
// told which of those happened, not a number.
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
			// Anything else is in flight, and settlePlugins would not
			// have returned; saying so here would be noise.
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
