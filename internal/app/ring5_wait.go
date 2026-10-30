package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
)

const (
	defaultRing5Retry    = 5 * time.Minute
	ring5RetryMaxSeconds = 60 * 60
)

var ring5RetryFirst = 10 * time.Second

func ring5Retry(g GenesisConfig) time.Duration {
	if n := g.Ring5RetrySeconds; n > 0 {
		return time.Duration(min(n, ring5RetryMaxSeconds)) * time.Second
	}
	return defaultRing5Retry
}

func notAnswered(err error) bool {
	var na *genesis.NotAnsweredError
	return errors.As(err, &na) && !refusedCertificate(err)
}

func refusedCertificate(err error) bool {
	fault := (*tlsfault.Fault)(nil)
	return errors.As(err, &fault)
}

func (a *App) waitForRing5(cfg Config, detail string) error {
	longest := ring5Retry(cfg.Genesis)
	first := min(ring5RetryFirst, longest)
	detail += fmt.Sprintf(". This start asks for it again by itself, first after %s and then waiting twice as long each time, up to %s (genesis.ring5_retry_seconds), and completes when it is fetched and verified", first, longest)
	logsink.Error("boot.refusal", "NO RING 5 WAS FETCHED — DEGRADED, waiting in the read-only posture and asking again: %s", detail)
	if err := a.startSafeBoot(safeRing5Unreached, detail); err != nil {
		return err
	}
	a.runBackground(func() { a.askForRing5(cfg.Genesis, first, longest) })
	return nil
}

func (a *App) askForRing5(g GenesisConfig, wait, longest time.Duration) {
	life := a.lifetime()
	for asked := 1; ; asked++ {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-life.Done():
			timer.Stop()
			return
		}
		_, err := g.client(life).FetchRing5()
		if life.Err() != nil {
			return
		}
		if notAnswered(err) {
			wait = min(wait*2, longest)
			logsink.Warn("ring.refusal", "Ring 5 is still not fetched (ask %d): %v — asked again in %s", asked, err, wait)
			continue
		}
		if err != nil {
			logsink.Info("safe.end", "a Ring 5 server answered (ask %d) and its answer, or the certificate on the way to it, was refused: %v — this start is made again, and that start says why", asked, err)
		} else {
			logsink.Info("safe.end", "DEGRADED→starting: Ring 5 was fetched and verified (ask %d) — this start is made again and completes", asked)
		}
		a.Restart()
		return
	}
}
