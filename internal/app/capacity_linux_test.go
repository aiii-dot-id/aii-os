//go:build linux

package app

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestMeminfoKeepsAMeasuredZeroApartFromNoAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		known       bool
		avail       int64
	}{
		{"an ordinary host", "MemTotal:       16384 kB\nMemFree:          100 kB\nMemAvailable:    8192 kB\n", true, 8192 << 10},
		{"nothing available", "MemTotal:       16384 kB\nMemAvailable:       0 kB\n", true, 0},
		{"a kernel without the line", "MemTotal:       16384 kB\nMemFree:          100 kB\n", false, 0},
		{"no table", "", false, 0},
	} {
		got := parseMeminfo(strings.NewReader(tc.table))
		if got.HostKnown != tc.known || (tc.known && (got.HostAvailable != tc.avail || got.HostTotal != 16384<<10)) {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}
