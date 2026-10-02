package app

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func afterRollback(rolled string, release func(), reexec func() error) error {
	if rolled == "" {
		return nil
	}
	logsink.Warn("updates.end", "rolled back to previous binary — re-executing the restored binary")
	release()
	if err := reexec(); err != nil {
		return fmt.Errorf("rolled back but could not re-exec the restored binary (refusing to continue on the failed image): %w", err)
	}
	return nil
}
