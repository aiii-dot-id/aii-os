package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestSafeCompletionTheRecordCannotVerifyFails(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetFrozen(true, "test: integrity lost")
	tr := appTranscript{st: st, safe: &safeToolRecord{}}
	// The record can no longer be asked anything.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	err = tr.RecordToolDone("turn-x", 1, "read", `{}`, conversation.Observation{Text: "result"})
	if err == nil {
		t.Fatal("a completion whose start could not be verified was accepted")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("the error must say the start is unverified: %v", err)
	}
}
