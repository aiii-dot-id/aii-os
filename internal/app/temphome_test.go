package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
func TestTempHomeDetection(t *testing.T) {
	for _, tc := range []struct {
		path string
		temp bool
	}{
		{"/tmp/aii-ui-smoke/data/ledger.jsonl", true},
		{"/tmp/x/ledger.jsonl", true},
		{"/home/user", false},
		{"/var/lib/aii/ledger.jsonl", false},
	} {
		home := filepath.Dir(tc.path)
		got := strings.HasPrefix(filepath.ToSlash(home), "/tmp/")
		if got != tc.temp {
			t.Errorf("%s: temp=%v want %v", tc.path, got, tc.temp)
		}
	}
}
