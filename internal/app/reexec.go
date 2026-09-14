package app

import (
	"fmt"
	"log"
)

// .
// .
// .
// .
// .
// .
// .
// .
func afterRollback(rolled string, reexec func() error) error {
	if rolled == "" {
		return nil
	}
	log.Printf("updates: rolled back to previous binary — re-executing the restored binary")
	if err := reexec(); err != nil {
		return fmt.Errorf("rolled back but could not re-exec the restored binary (refusing to continue on the failed image): %w", err)
	}
	return nil
}
