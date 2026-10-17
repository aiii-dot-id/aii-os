//go:build !windows

package app

import "testing"

// standAsTheCopy is nothing here: no number given again is told apart off
// Windows (handover_other.go), so a test's helper process, created after
// the test, is waited for as it stands.
func standAsTheCopy(*testing.T) {}
