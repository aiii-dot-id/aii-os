package app

import (
	"bytes"
	"regexp"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

var appGoroutineAllowance = map[string]string{}

var goroutineHeader = regexp.MustCompile(`^goroutine (\d+) \[`)

// appGoroutines returns the goroutines with a frame in this module's
// internal packages, keyed by id, each with its whole stack.
func appGoroutines() map[string]string {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	found := map[string]string{}
	for _, stack := range strings.Split(buf.String(), "\n\n") {
		m := goroutineHeader.FindStringSubmatch(stack)
		if m == nil || !strings.Contains(stack, "github.com/aiii-dot-id/aii-os/internal/") ||
			strings.Contains(stack, "internal/app.appGoroutines") {
			continue
		}
		found[m[1]] = stack
	}
	return found
}

func TestAStoppedAppAndItsSuccessorLeaveNoGoroutineBehind(t *testing.T) {
	before := appGoroutines()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Leaves")
	for _, which := range []string{"predecessor", "successor"} {
		a := New(safebootConfig(t, dir, "Leaves", keyPath, ledgerPath, dbPath))
		if err := startLiveForTest(a); err != nil {
			t.Fatalf("%s: startLive: %v", which, err)
		}
		if len(appGoroutines()) <= len(before) {
			t.Fatalf("%s: a live App started no goroutine of its own — the check would see nothing", which)
		}
		stopApp(t, a)
	}

	var left map[string]string
	var matched map[string]bool
	deadline := time.Now().Add(10 * time.Second)
	for {
		left, matched = map[string]string{}, map[string]bool{}
		for id, stack := range appGoroutines() {
			if _, old := before[id]; old {
				continue
			}
			known := false
			for site := range appGoroutineAllowance {
				if strings.Contains(stack, site) {
					matched[site], known = true, true
				}
			}
			if !known {
				left[id] = stack
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for id, stack := range left {
		t.Errorf("goroutine %s outlived its App's shutdown:\n%s", id, stack)
	}
	for site, owner := range appGoroutineAllowance {
		if !matched[site] {
			t.Errorf("the allowance for %s (%s) matches nothing any more — delete its line", site, owner)
		}
	}
}
