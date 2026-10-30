package app

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const (
	defaultWitnessAskAgain    = time.Hour
	witnessAskAgainMinSeconds = 60
	witnessAskAgainMaxSeconds = 24 * 60 * 60
)

func witnessAskAgain(w WitnessConfig) time.Duration {
	if n := w.AskAgainSeconds; n > 0 {
		return time.Duration(min(max(n, witnessAskAgainMinSeconds), witnessAskAgainMaxSeconds)) * time.Second
	}
	return defaultWitnessAskAgain
}

func witnessClientFor(cfg Config) *witness.Client {
	c := witness.New(cfg.Witness.URL, cfg.Witness.TLSSPKISHA256)
	c.SetGenesisURL(cfg.Genesis.ServerURL)
	return c
}

func (a *App) holdOnKeptWitnessAnswer(cfg Config, kp *crypto.KeyPair, t safeTrigger, detail string) error {
	if err := a.startSafeBoot(t, detail); err != nil {
		return err
	}
	if cfg.Witness.URL == "" {
		return nil
	}
	every := witnessAskAgain(cfg.Witness)

	anchorer := witness.NewAnchorer(witnessClientFor(cfg), a.ledger, witness.AsIdentityKey(kp), a.store, a.store, nil,
		cfg.Witness.IntervalEvents, cfg.Witness.PlatformPubkeyPath)
	a.runBackground(func() { a.askWitnessAgain(cfg, anchorer, every) })
	return nil
}

func (a *App) askWitnessAgain(cfg Config, anchorer *witness.Anchorer, every time.Duration) {
	life := a.lifetime()
	dir := filepath.Dir(cfg.Identity.LedgerPath)
	for asked := 1; ; asked++ {
		cleared, err := anchorer.AskWhileHeld(life)
		if life.Err() != nil {
			return
		}
		if cleared {
			now := time.Now().UTC()
			name, serr := witness.SetKeptConflictAside(dir, now)
			if serr != nil {
				logsink.Error("witness.error", "the witness at %s, asked again (ask %d), no longer shows the fork, and its kept answer could not be set aside (%v): SAFE holds; set %s aside by hand and restart", cfg.Witness.URL, asked, serr, witness.ConflictFileName)
				return
			}
			logsink.Warn("witness.decision", "the witness at %s, asked again (ask %d), no longer shows the fork: its answer kept beside the ledger is set aside as %s. SAFE holds until the identity is restarted, and the start after it takes the record through its checks", cfg.Witness.URL, asked, name)
			a.sayHeldCauseCleared(fmt.Sprintf("The witness at %s, asked again at %s, no longer shows that this record moved back or split; its earlier answer is set aside beside the record as %s. This start stays in SAFE: restart the identity, and the start takes the record through its checks.", cfg.Witness.URL, now.Format(time.RFC3339), name))
			return
		}
		logsink.Warn("witness.refusal", "the witness at %s, asked again (ask %d), still shows the fork or did not answer — SAFE holds, and it is asked again in %s: %v", cfg.Witness.URL, asked, every, err)
		timer := time.NewTimer(every)
		select {
		case <-timer.C:
		case <-life.Done():
			timer.Stop()
			return
		}
	}
}
