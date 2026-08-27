package app

import (
	"errors"
	"strings"
	"testing"
)

// rollback_reexec_test.go — R70: after a rollback, the restored
// artifact becomes the RUNNING artifact. The disk-only restore left
// the failed binary in charge until a human restarted it.

func TestAfterRollbackReexecsTheRestoredBinary(t *testing.T) {
	calls := 0
	if err := afterRollback("/data/aii.previous", func() error { calls++; return nil }); err != nil {
		t.Fatalf("re-exec path errored: %v", err)
	}
	if calls != 1 {
		t.Fatalf("re-exec ran %d times, want 1", calls)
	}
	calls = 0
	if err := afterRollback("", func() error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatalf("a boot with no rollback must not re-exec (calls=%d err=%v)", calls, err)
	}
}

func TestAfterRollbackRefusesToContinueOnFailedImage(t *testing.T) {
	err := afterRollback("/data/aii.previous", func() error { return errors.New("exec unavailable") })
	if err == nil {
		t.Fatal("re-exec failed and the boot continued on the failed image")
	}
	if !strings.Contains(err.Error(), "refusing to continue") {
		t.Fatalf("the refusal must say why: %v", err)
	}
}
