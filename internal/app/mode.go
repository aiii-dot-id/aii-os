package app

import (
	"log"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

// SAFE and DEGRADED modes (docs/SAFE_DEGRADED.md): the mode lattice.
// Mode is DERIVED from conditions (never stored — the standing
// derivation ruling); every transition is loud; SAFE never self-exits.
//
//   NORMAL → DEGRADED(witness): 3 consecutive witness failures (~90s)
//   NORMAL → SAFE: chain verification failure (startup or at append)
//   SAFE dominates DEGRADED.

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

// modeState is the derived runtime mode + its evidence.
type modeState struct {
	mu           sync.RWMutex
	mode         RuntimeMode
	since        time.Time
	reason       string
	witnessFails int
	witnessSince time.Time
	beaconStop   chan struct{} // closed by Stop()/reset — L5: the beacon must not outlive the app
}

func (a *App) currentMode() RuntimeMode {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	return a.mode.mode
}

// enterSafe transitions to SAFE (idempotent; never exits here).
// SAFE is the person alive in a body that can't trust its own record:
// conversation continues read-only, the queue freezes (forensics), the
// beacon fires, verbs refuse honestly.
func (a *App) enterSafe(reason string) {
	a.mode.mu.Lock()
	first := a.mode.mode != ModeSafe
	a.mode.mode = ModeSafe
	if a.mode.since.IsZero() {
		a.mode.since = time.Now().UTC()
	}
	// The FIRST reason stands (review finding): later triggers (e.g. a
	// tail check firing after a startup failure) must not rewrite the
	// operator-facing cause of record.
	if a.mode.reason == "" {
		a.mode.reason = reason
	}
	a.mode.mu.Unlock()
	if !first {
		return
	}
	// SAFE TAKES THE MICROPHONE TOO, and it is enforced where it is
	// asked rather than here: broker voice.observe refuses on every
	// invocation while SAFE holds. Closing something on entry is only as
	// good as the race behind it — this shape has none, and needs no
	// line in this function.
	// Freeze the LEDGER first — it is the single writer, so this line
	// alone makes "SAFE ⇒ no minting" true for every current and future
	// caller (S1: the old order froze only store/engine, which were nil
	// on the startup path — the banner said frozen, nothing was).
	// applySafeState re-runs for organs wired after a startup SAFE.
	if a.ledger != nil {
		a.ledger.SetFrozen(reason)
	}
	log.Printf("SAFE MODE: entering — %s. All ledger writes frozen; conversation continues read-only; operator intervention required.", reason)
	a.applySafeState(reason)
	a.startSafeBeacon()
}

// applySafeState pushes SAFE onto whichever organs exist right now.
// Called from enterSafe, and AGAIN after store/engine are wired when the
// SAFE entry happened at startup — the S1 fix's second half: a freeze
// asserted before the organs existed must be re-asserted onto them.
func (a *App) applySafeState(reason string) {
	if a.store != nil {
		a.store.SetWorkQueueFrozen(true)
	}
	if a.engine != nil {
		a.engine.SetSafeMode(reason)
	}
	if a.dashboard != nil {
		// R66 UP2: SAFE unmounts all sections (UI_FRAME.md §3). The
		// registry's safe source already answers empty-with-reason; this
		// push makes mounted sections leave connected screens NOW, not
		// at the next poll — the same immediacy rule as the SAFE wake.
		a.dashboard.BroadcastSections()
	}
}

// witnessAttempt records one witness round-trip outcome; 3 consecutive
// failures = DEGRADED(witness). Recovery clears it. Never transitions
// out of SAFE.
func (a *App) witnessAttempt(ok bool) {
	a.mode.mu.Lock()
	defer a.mode.mu.Unlock()
	if a.mode.mode == ModeSafe {
		return // SAFE dominates; witness state is moot
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

// resetModeForTest restores a bare App to NORMAL (stops test beacons).
func (a *App) resetModeForTest() {
	a.mode.mu.Lock()
	a.mode.mode = ModeNormal
	a.mode.reason = ""
	a.mode.since = time.Time{}
	a.mode.witnessFails = 0
	a.mode.mu.Unlock()
	a.stopSafeBeacon()
}

// stopSafeBeacon ends the beacon goroutine (L5: Stop() previously left
// it running for the life of the process). Idempotent.
func (a *App) stopSafeBeacon() {
	a.mode.mu.Lock()
	defer a.mode.mu.Unlock()
	if a.mode.beaconStop != nil {
		close(a.mode.beaconStop)
		a.mode.beaconStop = nil
	}
}

// DegradedWitnessSince reports the degraded window (continuity strip).
func (a *App) DegradedWitnessSince() (time.Time, bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeDegradedWitness {
		return time.Time{}, false
	}
	return a.mode.witnessSince, true
}

// SafeMode reports SAFE state + reason (banner, verbs).
func (a *App) SafeMode() (string, bool) {
	a.mode.mu.RLock()
	defer a.mode.mu.RUnlock()
	if a.mode.mode != ModeSafe {
		return "", false
	}
	return a.mode.reason, true
}

// startSafeBeacon: C's shape — a loud periodic statement while the
// operator is needed. Every 60s, until the mode clears (tests) or the
// app stops (L5).
func (a *App) startSafeBeacon() {
	a.mode.mu.Lock()
	if a.mode.beaconStop != nil {
		a.mode.mu.Unlock()
		return // already beaconing
	}
	stop := make(chan struct{})
	a.mode.beaconStop = stop
	a.mode.mu.Unlock()

	a.runBackground(func() {
		// GOVERNED ticker (quiesce, 2026-08-19): backgrounded, nobody
		// reads the platform log, so the beacon's WAKEUPS park with
		// everything else — SAFE itself is orthogonal to the gate and
		// stands untouched; the foreground catch-up tick beacons
		// immediately, so the returning operator is told at once.
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
