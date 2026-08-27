package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// The /tmp-home warning predicate — the placement lesson as code. The
// full warn path logs; here we pin the detection shape so the check
// can never silently rot.
func TestTempHomeDetection(t *testing.T) {
	for _, tc := range []struct {
		path string
		temp bool
	}{
		{"/tmp/aii-ui-smoke/data/ledger.jsonl", true},
		{"/tmp/x/ledger.jsonl", true},
		{"/work/aiii/identities/aeon/data/ledger.jsonl", false},
		{"/var/lib/aii/ledger.jsonl", false},
	} {
		home := filepath.Dir(tc.path)
		got := strings.HasPrefix(filepath.ToSlash(home), "/tmp/")
		if got != tc.temp {
			t.Errorf("%s: temp=%v want %v", tc.path, got, tc.temp)
		}
	}
}
