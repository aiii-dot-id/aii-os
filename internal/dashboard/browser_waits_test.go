//go:build !windows

package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func unexplainedWaits(source string) []string {
	const tick = 50 // ms
	delay := regexp.MustCompile(`setTimeout\([^,]*,\s*(\d+)\s*\)`)
	var found []string
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		m := delay.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ms, err := strconv.Atoi(m[1])
		if err != nil || ms <= tick {
			continue
		}
		justified := strings.Contains(line, "waits by design:")
		for back := 1; back <= 3 && i-back >= 0 && !justified; back++ {
			justified = strings.Contains(lines[i-back], "waits by design:")
		}
		if !justified {
			found = append(found, fmt.Sprintf("%d: %s", i+1, strings.TrimSpace(line)))
		}
	}
	return found
}

// THE RULE CATCHES WHAT IT IS FOR. A detector is only worth the cases it
// would report, so it is shown one of each first: a page that waits by the
// clock with nothing said, and the same wait with its reason. Without this,
// weakening the rule breaks nothing — the repository passes it either way,
// which is exactly how a guard rots.
func TestTheFixedWaitRuleCatchesAFixedWait(t *testing.T) {
	bad := "  await new Promise(r => setTimeout(r, 400));"
	if got := unexplainedWaits(bad); len(got) != 1 {
		t.Errorf("a 400ms wait with nothing said was not reported: %v", got)
	}
	sameLine := "  await new Promise(r => setTimeout(r, 400)); // waits by design: the hold"
	if got := unexplainedWaits(sameLine); len(got) != 0 {
		t.Errorf("a justified wait was reported: %v", got)
	}
	above := "  // waits by design: the press is held past its threshold\n  await new Promise(r => setTimeout(r, 450));"
	if got := unexplainedWaits(above); len(got) != 0 {
		t.Errorf("a wait justified on the line above was reported: %v", got)
	}
	far := "  // waits by design: too far above to be about this line\n  a();\n  b();\n  c();\n  await new Promise(r => setTimeout(r, 450));"
	if got := unexplainedWaits(far); len(got) != 1 {
		t.Errorf("a justification four lines up was accepted for this wait: %v", got)
	}
	tick := "  const tick = () => new Promise(r => setTimeout(r, 15));"
	if got := unexplainedWaits(tick); len(got) != 0 {
		t.Errorf("a poll's own interval was reported as a fixed wait: %v", got)
	}
}

func TestBrowserPagesWaitForConditionsNotDurations(t *testing.T) {
	files, err := filepath.Glob("*_browser_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("only %d browser pages found: this rule is not looking where the pages are", len(files))
	}
	for _, name := range files {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, where := range unexplainedWaits(string(raw)) {
			t.Errorf("%s:%s\n  waits by the clock instead of for what it is about. Use until/when from\n"+
				"  /__harness.js, or — if the DURATION is the point, as a press held past its\n"+
				"  threshold is — say so on the line above: \"// waits by design: <why>\".", name, where)
		}
	}
}
