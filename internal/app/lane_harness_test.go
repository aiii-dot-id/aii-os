package app

import (
	"os"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestMain(m *testing.M) {
	harnessLane = func() (string, []string, error) { return "", nil, nil }
	os.Exit(m.Run())
}
