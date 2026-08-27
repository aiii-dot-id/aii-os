package app

import (
	"fmt"
	"log"
)

// afterRollback is the R70 decision, separated so a test can hold it:
// a boot that just restored the previous binary must not CONTINUE as
// the failed image — until 2026-08-26 the failed binary finished
// booting with full authority and stayed in charge until someone
// restarted it (external review P1-2). The restored artifact becomes
// the running artifact NOW, by re-exec; and if the re-exec mechanism
// itself fails, the boot fails — down-and-restartable is honest,
// running-known-bad is not (R70 amendment, operator-ordered).
func afterRollback(rolled string, reexec func() error) error {
	if rolled == "" {
		return nil
	}
	log.Printf("updates: rolled back to previous binary — re-executing the restored binary (R70)")
	if err := reexec(); err != nil {
		return fmt.Errorf("R70: rolled back but could not re-exec the restored binary (refusing to continue on the failed image): %w", err)
	}
	return nil
}
