package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func (l *ledgerAdapter) appendAsAnEarlierRelease(t *testing.T, eventType ledger.EventType, ring int, payload any) *ledger.Event {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	prepared, err := l.Ledger.PreparePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	evt, err := l.Ledger.AppendPrepared(eventType, ring, prepared, l.kp)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.st.Materialize(evt); err != nil {
		t.Fatalf("an earlier release's record did not materialize: %v", err)
	}
	if l.onAppend != nil {
		l.onAppend(evt)
	}
	return evt
}
