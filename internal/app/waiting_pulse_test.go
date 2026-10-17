package app

import (
	"math"
	"slices"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
)

func TestAWaitingPulseIsNotCarriedToTheReflectiveCadences(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Rhythm")
	boot := safebootConfig(t, dir, "Rhythm", keyPath, ledgerPath, dbPath)
	boot.Prompt.PulseIntervalSeconds = 120
	a := New(boot)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	every := func() int64 {
		t.Helper()
		rows, err := a.store.DueAlarms("life", math.MaxInt64, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.AlarmID == cognitive.ReflectSelfModelAlarm && r.RepeatEvery != nil {
				return *r.RepeatEvery
			}
		}
		t.Fatal("the portrait has no alarm")
		return 0
	}
	card := func() int { return a.configState().Cognition.PulseSeconds }
	// 250 lived minutes at a 120-second pulse are 125 pulses.
	if every() != 125 || card() != 120 {
		t.Fatalf("rig: started at a pulse of 120 seconds, the portrait is armed every %d pulses and the card states %d", every(), card())
	}

	st, err := a.applyConfigChange(map[string]any{"prompt.pulse_interval_seconds": float64(60)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(st.RestartRequired, "prompt.pulse_interval_seconds") {
		t.Fatalf("rig: the pulse is saved for the next start: %v", st.RestartRequired)
	}
	// THE DOOR: another setting saved, then the portrait's own minutes.
	for _, change := range []map[string]any{
		{"timezone": "Asia/Tokyo"},
		{"cognition.consolidate_spacing_seconds": float64(2400)},
	} {
		if _, err = a.applyConfigChange(change); err != nil {
			t.Fatal(err)
		}
		if got := every(); got != 125 {
			t.Fatalf("WITH A PULSE OF 60 SECONDS WAITING FOR A RESTART, SAVING %v ARMED THE PORTRAIT EVERY %d PULSES OF THE 120-SECOND HEARTBEAT; its 250 lived minutes are 125", change, got)
		}
	}
	if _, err = a.applyConfigChange(map[string]any{"cognition.self_model_every_minutes": float64(100)}); err != nil {
		t.Fatal(err)
	}
	if got := every(); got != 50 {
		t.Fatalf("through the door, 100 lived minutes at the 120-second pulse in force is 50 pulses; the portrait is armed every %d", got)
	}
	// A RELOAD: the file states the portrait's minutes anew, the pulse of
	// 60 still waiting in it.
	edited := a.configSnapshot()
	edited.Cognition.SelfModelEveryMinutes = 200
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := every(); got != 100 {
		t.Fatalf("AT A RELOAD, 200 lived minutes at the 120-second pulse in force is 100 pulses; the portrait is armed every %d (200 is the count at the pulse that waits)", got)
	}
	// And the card says the pulse in force, not the one that waits.
	if card() != 120 {
		t.Fatalf("the card states a pulse of %d seconds in force; the heartbeat runs on 120 until a restart", card())
	}
}
