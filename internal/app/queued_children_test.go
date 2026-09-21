package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
func TestComposeShowsQueuedChildrenApart(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	a.store = st
	for _, ws := range []string{"ws_run", "ws_wait"} {
		if _, enq, err := st.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: "{}", DedupKey: ws, Source: "identity"},
			4, ws, store.SubagentDescription("goal of "+ws)); err != nil || !enq {
			t.Fatalf("enqueue %s: %v %v", ws, enq, err)
		}
	}
	if it, err := st.ClaimWork([]string{identity.SubagentWorkKind}, time.Now().UnixMilli()); err != nil || it == nil || it.DedupKey != "ws_run" {
		t.Fatalf("claim: %+v %v", it, err)
	}
	state, err := a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "1 running, 1 queued") {
		t.Fatalf("header must count running and queued apart:\n%s", state)
	}
	if !strings.Contains(state, "ws_wait: goal of ws_wait (queued — starts when a slot frees)") {
		t.Fatalf("the queued child must be marked:\n%s", state)
	}
	if strings.Contains(state, "ws_run: goal of ws_run (queued") {
		t.Fatalf("the running child must not be marked queued:\n%s", state)
	}
}
