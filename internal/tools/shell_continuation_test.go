//go:build !windows

package tools

import (
	"reflect"
	"testing"
)

func TestABackslashNewlineIsRemovedAsBashRemovesIt(t *testing.T) {
	r, home := heredocHome(t)
	for _, cmd := range []string{
		"cat aii\\\n.db",
		"cat \"aii\\\n.db\"",
		"cat $\"aii\\\n.db\"",
		"cat $'\\x61'i\\\ni.db",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why == "" {
			t.Errorf("A READ OF THE STORE WAS ADMITTED: %q", cmd)
		}
	}
	for in, want := range map[string][]string{
		"cat aii\\\n.db":        {"cat", "aii.db"},
		"cat \"aii\\\n.db\"":    {"cat", "aii.db"},
		"cat $\"aii\\\n.db\"":   {"cat", "aii.db"},
		"cat $'\\x61'i\\\ni.db": {"cat", "aii.db"},
		"cat 'aii\\\n.db'":      {"cat", "aii\\\n.db"},
		"cat $'aii\\\n.db'":     {"cat", "aii\\\n.db"},
		"echo a \\\n b":         {"echo", "a", "b"},
	} {
		for _, v := range []bashVersion{bash3, bash5} {
			if got := shellWords(in, v); !reflect.DeepEqual(got, want) {
				t.Errorf("shellWords(%q, %d) = %q, want %q", in, v, got, want)
			}
		}
	}
}
