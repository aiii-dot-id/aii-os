//go:build !windows

package tools

import (
	"context"
	"path/filepath"
	"testing"
)

// THE EXEMPTION IS THE SAME BEHIND A WALL THAT HOLDS THE KEPT PLACES AND
// BEHIND ONE THAT DOES NOT.
//
// The ordinary-account wall layer is not covered by the gate's fixtures by
// accident: this suite runs as root on the gate's host, where shellWallKeepsPlaces() is false throughout, so every
// other test in this package exercises one branch only. keepsPlaces pins
// the predicate for one test wherever it runs, so both branches are
// reachable from here.
//
// What is asserted is the exemption's independence of that predicate: the
// same words are exempt and the same real paths refuse, whichever way the
// wall state is pinned. shellPathlessWords reads only the command and its
// tokens, so this should hold -- and a test is how I know rather than
// assert it.
func TestTheExemptionDoesNotDependOnWhetherTheWallHoldsTheKeptPlaces(t *testing.T) {
	ctx := context.Background()

	// The words a program's grammar never reads as a path. They must be
	// admitted in both wall states.
	words := []string{
		"git log", "git log --oneline -1", "git log --format=%s",
		"go build ./...", "docker ps",
	}

	// The paths that must refuse in both wall states.
	real := []string{
		"ls log", "cat log/today", "mv log x", "rm -r log", "cp log x",
		"cat vault.key", "rm vault.key", "mv vault.key x",
		"git log -- log", "git log log", "git log > log", "git < log",
		"git log; rm -r log", "git log && rm -r log", "cd log && rm *",
		"echo log | sh", "git log --oneline -1 log",
	}

	for _, walled := range []bool{false, true} {
		keepsPlaces(t, walled)

		r, _, home := newWordWall(t)
		work := filepath.Join(home, "work")

		for _, cmd := range words {
			for _, args := range []map[string]any{
				{"command": cmd},
				{"command": cmd, "cwd": work},
			} {
				res, err := r.Execute(ctx, "shell", args)
				if err != nil {
					t.Fatalf("walled=%v %q: %v", walled, cmd, err)
				}
				if res.Refused {
					t.Errorf("walled=%v: %q was refused as a path, but it names no path (cwd=%v)",
						walled, cmd, args["cwd"] != nil)
				}
			}
		}

		for _, cmd := range real {
			for _, args := range []map[string]any{
				{"command": cmd},
				{"command": cmd, "cwd": work},
			} {
				res, err := r.Execute(ctx, "shell", args)
				if err != nil {
					t.Fatalf("walled=%v %q: %v", walled, cmd, err)
				}
				if !res.Refused {
					t.Errorf("walled=%v: %q was let through (cwd=%v)",
						walled, cmd, args["cwd"] != nil)
				}
			}
		}
	}
}
