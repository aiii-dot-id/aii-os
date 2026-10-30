package tools

import (
	"strings"
	"testing"
)

func TestARelaysFailureIsReadByItsMarkAlone(t *testing.T) {
	const mark = "aii-relay-THISCALL"
	const constrained = "this machine's PowerShell runs in Constrained Language mode (the words)"
	start := shellStart{mark: mark, constrained: constrained}
	for _, c := range []struct {
		name, output string
		s            shellStart
		want         string
		failed       bool
	}{
		{"a start that throws", mark + " start The system cannot find the file specified.\r\n", start,
			"the host's starter could not start your shell: The system cannot find the file specified. Nothing of your command ran.", true},
		{"after a byte-order mark", "\ufeff" + mark + " start Access is denied\r\n", start,
			"the host's starter could not start your shell: Access is denied. Nothing of your command ran.", true},
		{"after other output", "something\n" + mark + " start The filename or extension is too long.\n", start,
			"the host's starter could not start your shell: The filename or extension is too long. Nothing of your command ran.", true},
		{"a reason that is not UTF-8", mark + " start caf\x82\n", start,
			"the host's starter could not start your shell: caf\uFFFD. Nothing of your command ran.", true},
		{"Constrained Language", mark + " language\r\n", start, constrained, true},
		{"the command's own output", "hello\n[exit status 1]", start, "", false},
		{"another call's mark", "aii-relay-OTHERCALL start nope\n", start, "", false},
		{"the mark without its kind", mark + "\n", start, "", false},
		{"a start with no mark", mark + " start nope\n", shellStart{}, "", false},
	} {
		got, failed := c.s.relayFailure([]byte(c.output))
		if failed != c.failed || got != c.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, failed, c.want, c.failed)
		}
	}
	if strings.Contains(shellRelayFailed("x"), "..") {
		t.Errorf("a reason ending in a full stop is stopped twice")
	}
}
