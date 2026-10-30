package app

import (
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/hold"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
)

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
		return "SAFE (read-only)"
	default:
		return "NORMAL"
	}
}

type modeState struct {
	mu                  sync.RWMutex
	mode                RuntimeMode
	since               time.Time
	reason              string
	trigger             safeTrigger
	detail              string
	cleared             string
	witnessFails        int
	witnessSince        time.Time
	witnessWhy          string
	witnessRefusedSince time.Time
	clock               func() time.Time
	beaconStop          chan struct{}
}

func (m *modeState) now() time.Time {
	if m.clock != nil {
		return m.clock().UTC()
	}
	return time.Now().UTC()
}

func (a *App) currentMode() RuntimeMode {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	return a.mode.mode
}

func (a *App) enterSafe(reason string) { a.enterSafeFor(0, reason) }

func (a *App) enterSafeFor(t safeTrigger, detail string) {
	reason := safeReason(t, detail)
	a.mode.mu.Lock()
	first := a.mode.mode != ModeSafe
	a.mode.mode = ModeSafe
	if a.mode.since.IsZero() {
		a.mode.since = time.Now().UTC()
	}

	if a.mode.reason == "" {
		a.mode.reason, a.mode.trigger, a.mode.detail = reason, t, detail
	}
	a.mode.mu.Unlock()
	if !first {
		return
	}

	if a.ledger != nil {
		a.ledger.SetHold(hold.Hold{Reason: reason, Waiting: safePostures[t].degraded})
	}

	degraded := safePostures[t].degraded
	if degraded {
		logsink.Warn("safe.decision", "DEGRADED — %s. This start holds the read-only posture until it completes, which it does by itself when Ring 5 is fetched and verified.", reason)
	} else {
		logsink.Error("safe.start", "entering — %s. All ledger writes frozen; conversation continues read-only; operator intervention required.", reason)
	}
	a.applySafeState(reason)

	if reg := a.toolReg; reg != nil {
		a.runBackground(func() { a.endLeftJobs(reg, "safe.start") })
	}

	a.pluginMu.Lock()
	f := a.facility
	a.pluginMu.Unlock()
	if f != nil {
		f.Hold(safeHoldSentence(reason))
	}
	a.pokePluginSweep()
	if !degraded {
		a.startSafeBeacon()
	}
}

func (a *App) integrityLost(t safeTrigger, err error) { a.enterSafeFor(t, err.Error()) }

func (a *App) applySafeState(reason string) {
	h := hold.Safe(reason)
	if held, ok := a.Hold(); ok && held.Reason == reason {
		h = held
	}
	if a.store != nil {

		a.store.SetHold(true, h)
	}
	if a.engine != nil {
		a.engine.SetHold(h)
	}

	a.holdSandboxInSafe()

	a.abortVoiceSessionsUnderSafe(reason)
	if a.dashboard != nil {

		a.dashboard.BroadcastSections()
	}
}

func (a *App) witnessAttempt(failed error) {
	a.mode.mu.Lock()
	defer a.mode.mu.Unlock()
	if a.mode.mode == ModeSafe {
		return
	}
	if failed == nil {
		switch {
		case a.mode.mode == ModeDegradedWitness && a.mode.witnessWhy == "":
			logsink.Info("safe.end", "DEGRADED→NORMAL: witness reachable again (was dark since %s)", a.mode.witnessSince.Format("15:04:05 MST"))
		case a.mode.mode == ModeDegradedWitness:
			logsink.Info("safe.end", "DEGRADED→NORMAL: the witness answered under a certificate this machine accepts (none was accepted since %s)", a.mode.witnessSince.Format("15:04:05 MST"))
		case a.mode.witnessWhy != "":

			logsink.Info("safe.end", "the witness answered under a certificate this machine accepts: the refusal since %s no longer holds", a.mode.witnessRefusedSince.Format("15:04:05 MST"))
		}
		if a.mode.mode == ModeDegradedWitness {
			a.mode.mode = ModeNormal
		}
		a.mode.witnessFails, a.mode.witnessWhy = 0, ""
		return
	}
	why := ""
	if fault := (*tlsfault.Fault)(nil); errors.As(failed, &fault) {
		why = fault.Error()
		if a.mode.witnessWhy == "" {
			a.mode.witnessRefusedSince = a.mode.now()
		}
	}
	a.mode.witnessWhy = why
	a.mode.witnessFails++
	if a.mode.witnessFails == 1 {
		a.mode.witnessSince = a.mode.now()
	}
	if a.mode.witnessFails >= 3 && a.mode.mode != ModeDegradedWitness {
		a.mode.mode = ModeDegradedWitness
		a.mode.since = time.Now().UTC()
		if a.mode.witnessWhy == "" {
			logsink.Warn("safe.decision", "DEGRADED (witness): unreachable since %s (%d attempts) — anchoring paused; everything else normal; auto-recovers on success.",
				a.mode.witnessSince.Format("15:04:05 MST"), a.mode.witnessFails)
		} else {
			logsink.Warn("safe.decision", "DEGRADED (witness): no accepted answer since %s (%d attempts); the last attempt met a refused certificate: %s — anchoring paused; everything else normal; auto-recovers on success.",
				a.mode.witnessSince.Format("15:04:05 MST"), a.mode.witnessFails, a.mode.witnessWhy)
		}
	}
}

func (a *App) stopSafeBeacon() {
	a.mode.mu.Lock()
	defer a.mode.mu.Unlock()
	if a.mode.beaconStop != nil {
		close(a.mode.beaconStop)
		a.mode.beaconStop = nil
	}
}

func (a *App) DegradedWitnessSince() (since time.Time, why string, degraded bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeDegradedWitness {
		return time.Time{}, "", false
	}
	if a.mode.witnessWhy != "" {
		return a.mode.witnessRefusedSince, a.mode.witnessWhy, true
	}
	return a.mode.witnessSince, "", true
}

func (a *App) safeCause() (safeTrigger, string, bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	return a.mode.trigger, a.mode.detail, a.mode.mode == ModeSafe
}

func (a *App) sayHeldCauseCleared(words string) {
	a.mode.mu.Lock()
	safe := a.mode.mode == ModeSafe
	if safe {
		a.mode.cleared = words
	}
	a.mode.mu.Unlock()
	if safe && a.dashboard != nil {
		a.dashboard.BroadcastContinuity()
	}
}

func (a *App) heldCauseCleared() string {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeSafe {
		return ""
	}
	return a.mode.cleared
}

func (a *App) Hold() (hold.Hold, bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeSafe {
		return hold.Hold{}, false
	}
	return hold.Hold{Reason: a.mode.reason, Waiting: safePostures[a.mode.trigger].degraded}, true
}

func (a *App) whileHeld() string {
	if h, _ := a.Hold(); h.Waiting {
		return hold.While
	}
	return "while the identity is in SAFE"
}

func (a *App) SafeMode() (string, bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeSafe {
		return "", false
	}
	return a.mode.reason, true
}

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
			logsink.Error("safe.decision", "BEACON: writes denied for %s — reason: %s. Conversation is read-only; queue frozen (forensic snapshot preserved); operator intervention required.",
				time.Since(since).Round(time.Second), reason)
			select {
			case <-t.C:
			case <-stop:
				return
			}
		}
	})
}
