package app

import (
	"log"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

// .
// .
// .
// .
// .
// .
// .

type RuntimeMode int

const (
	ModeNormal RuntimeMode = iota
	ModeDegradedWitness
	ModeSafe
)

func (m RuntimeMode) String() string {
	switch m {
	case ModeDegradedWitness:
		return "DEGRADED (witness unreachable)"
	case ModeSafe:
		return "SAFE (integrity compromised — read-only)"
	default:
		return "NORMAL"
	}
}

// .
type modeState struct {
	mu           sync.RWMutex
	mode         RuntimeMode
	since        time.Time
	reason       string
	witnessFails int
	witnessSince time.Time
	beaconStop   chan struct{}
}

func (a *App) currentMode() RuntimeMode {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	return a.mode.mode
}

// .
// .
// .
// .
func (a *App) enterSafe(reason string) {
	a.mode.mu.Lock()
	first := a.mode.mode != ModeSafe
	a.mode.mode = ModeSafe
	if a.mode.since.IsZero() {
		a.mode.since = time.Now().UTC()
	}
	// .
	// .
	// .
	if a.mode.reason == "" {
		a.mode.reason = reason
	}
	a.mode.mu.Unlock()
	if !first {
		return
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
	if a.ledger != nil {
		a.ledger.SetFrozen(reason)
	}
	log.Printf("SAFE MODE: entering — %s. All ledger writes frozen; conversation continues read-only; operator intervention required.", reason)
	a.applySafeState(reason)
	// .
	// .
	// .
	// .
	// .
	// .
	a.pluginMu.Lock()
	f := a.facility
	a.pluginMu.Unlock()
	if f != nil {
		f.Hold(safeHoldSentence(reason))
	}
	a.pokePluginSweep()
	a.startSafeBeacon()
}

// .
// .
// .
// .
func (a *App) applySafeState(reason string) {
	if a.store != nil {
		a.store.SetWorkQueueFrozen(true)
	}
	if a.engine != nil {
		a.engine.SetSafeMode(reason)
	}
	// .
	// .
	// .
	// .
	a.abortVoiceSessionsUnderSafe(reason)
	if a.dashboard != nil {
		// .
		// .
		// .
		// .
		a.dashboard.BroadcastSections()
	}
}

// .
// .
// .
func (a *App) witnessAttempt(ok bool) {
	a.mode.mu.Lock()
	defer a.mode.mu.Unlock()
	if a.mode.mode == ModeSafe {
		return
	}
	if ok {
		if a.mode.mode == ModeDegradedWitness {
			log.Printf("DEGRADED→NORMAL: witness reachable again (was dark since %s)", a.mode.witnessSince.Format("15:04:05 MST"))
			a.mode.mode = ModeNormal
		}
		a.mode.witnessFails = 0
		return
	}
	a.mode.witnessFails++
	if a.mode.witnessFails == 1 {
		a.mode.witnessSince = time.Now().UTC()
	}
	if a.mode.witnessFails >= 3 && a.mode.mode != ModeDegradedWitness {
		a.mode.mode = ModeDegradedWitness
		a.mode.since = time.Now().UTC()
		log.Printf("DEGRADED (witness): unreachable since %s (%d attempts) — anchoring paused; everything else normal; auto-recovers on success.",
			a.mode.witnessSince.Format("15:04:05 MST"), a.mode.witnessFails)
	}
}

// .
func (a *App) resetModeForTest() {
	a.mode.mu.Lock()
	a.mode.mode = ModeNormal
	a.mode.reason = ""
	a.mode.since = time.Time{}
	a.mode.witnessFails = 0
	a.mode.mu.Unlock()
	a.stopSafeBeacon()
}

// .
// .
func (a *App) stopSafeBeacon() {
	a.mode.mu.Lock()
	defer a.mode.mu.Unlock()
	if a.mode.beaconStop != nil {
		close(a.mode.beaconStop)
		a.mode.beaconStop = nil
	}
}

// .
func (a *App) DegradedWitnessSince() (time.Time, bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeDegradedWitness {
		return time.Time{}, false
	}
	return a.mode.witnessSince, true
}

// .
func (a *App) SafeMode() (string, bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeSafe {
		return "", false
	}
	return a.mode.reason, true
}

// .
// .
// .
func (a *App) startSafeBeacon() {
	a.mode.mu.Lock()
	if a.mode.beaconStop != nil {
		a.mode.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	a.mode.beaconStop = stop
	a.mode.mu.Unlock()

	a.runBackground(func() {
		// .
		// .
		// .
		// .
		// .
		t := quiesce.NewTicker(a.gate, 60*time.Second)
		defer t.Stop()
		for {
			a.mode.mu.RLock()
			still := a.mode.mode == ModeSafe
			since := a.mode.since
			reason := a.mode.reason
			a.mode.mu.RUnlock()
			if !still {
				return
			}
			log.Printf("SAFE MODE BEACON: writes denied for %s — reason: %s. Conversation is read-only; queue frozen (forensic snapshot preserved); operator intervention required.",
				time.Since(since).Round(time.Second), reason)
			select {
			case <-t.C:
			case <-stop:
				return
			}
		}
	})
}
