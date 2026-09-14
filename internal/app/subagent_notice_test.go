package app

import (
	"strings"
	"testing"
)

// .
// .
// .
func TestSubagentNoticeIsOneLineAndNeverTheReport(t *testing.T) {
	long := strings.Repeat("audit the platform ", 20)
	for _, c := range []struct {
		name, outcome, want string
	}{
		{"served", "served: all beans counted\n[sub-agent role=\"\" model=\"m\"]\n# VERDICT", "— served"},
		{"partial rounds", "partial: UNFINISHED — the sub-agent reached its round ceiling before it was done. What follows is real but partial; decide whether to spawn a continuation.\n\nbody", "— partial (rounds)"},
		{"partial calls", "partial: UNFINISHED — the sub-agent reached its tool-call budget before it was done; calls in its final batch were not executed.\n\nbody", "— partial (calls)"},
		{"unserved failed", "unserved: FAILED: boom", "— unserved (failed)"},
		{"the child's own verdict", "partial: tests written, integration blocked\n# VERDICT\nmore", "— partial"},
		{"pre-start marker", "unserved: failed before start: compose blew up", "— unserved (failed)"},
	} {
		got := subagentNotice(long, c.outcome)
		if !strings.HasSuffix(got, c.want) {
			t.Errorf("%s: notice %q does not end with %q", c.name, got, c.want)
		}
		if strings.Contains(got, "\n") || len([]rune(got)) > 170 {
			t.Errorf("%s: notice is not one short line: %q", c.name, got)
		}
		for _, leak := range []string{"UNFINISHED —", "# VERDICT", "[sub-agent role="} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: the report leaked into the notice: %q", c.name, leak)
			}
		}
	}
}
