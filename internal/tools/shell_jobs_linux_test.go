//go:build linux && !android

package tools

import "testing"

// The name of a process may hold spaces and parentheses, and stands
// before the fields this reads: a name written to look like a status
// line must not be read as one.
func TestAProcessStatusLineIsReadFromItsLastParenthesis(t *testing.T) {
	const rest = " 1 777 777 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 123456 0 0"
	for _, c := range []struct {
		name, line string
		pgid       int
		started    uint64
		live       bool
	}{
		{"a plain name", "42 (sleep) S" + rest, 777, 123456, true},
		{"a name that looks like the line", "42 (a) R 1 9 9 0) S" + rest, 777, 123456, true},
		{"exited and not yet reaped", "42 (sleep) Z" + rest, 0, 0, false},
		{"cut short", "42 (sleep) S 1 777", 0, 0, false},
		{"not a status line", "", 0, 0, false},
	} {
		pgid, started, live := procStat([]byte(c.line))
		if pgid != c.pgid || started != c.started || live != c.live {
			t.Errorf("%s: group %d started %d live %v, want %d %d %v", c.name, pgid, started, live, c.pgid, c.started, c.live)
		}
	}
}
