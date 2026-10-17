//go:build !windows

package app

import (
	"os"
	"testing"
)

// unlistable leaves at path something no listing of it can get past: a
// file where the directory belongs fails the listing for every user, root
// included — a permission bit is not a failure to the root these gates
// run as.
func unlistable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
}
