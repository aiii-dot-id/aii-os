package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestDeliveryDuringATurnIsHarvestedAfterItEnds(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "join probe"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	const workerID = "ws_joinprobe"
	queueChild(t, app.store, workerID)
	landed := make(chan struct{})

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1:
			// A worker finishes WHILE the parent holds the gate. Its
			// own wake would wait 30s and drop; here it never even
			// gets scheduled, which is the harsher case.
			if err := app.store.StartWorkSession(t.Context(), workerID, store.SubagentDescription("count the beans")); err != nil {
				t.Error(err)
			}
			if err := app.store.DeliverWorkSession(t.Context(), workerID, "beans counted: 41", "", ""); err != nil {
				t.Error(err)
			}
			close(landed)
			_, _ = io.WriteString(w, callReply("", toolCall("c1", "read", string(readArgs))))
		case n == 2:
			_, _ = io.WriteString(w, textReply("parent turn done"))
		default:
			// Every later request is the self-scheduled harvest turn.
			_, _ = io.WriteString(w, textReply("harvested the outcome"))
		}
	}))
	defer srv.Close()

	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, "do a little work", func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	<-landed

	// NOTHING further is said to the identity. The sweep at the turn's
	// end is the only thing that can move this.
	deadline := time.Now().Add(30 * time.Second)
	var harvested int64
	for time.Now().Before(deadline) {
		if err := app.store.DB().QueryRow(`SELECT COALESCE(harvested_ms,0) FROM work_sessions WHERE id=?`, workerID).
			Scan(&harvested); err != nil {
			t.Fatal(err)
		}
		if harvested != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if harvested == 0 {
		t.Fatalf("the delivery was never harvested — a turn that ends does not check its own mail, so yielding the gate frees it for nobody (requests seen: %d)", len(obs.snapshot()))
	}
}

// The sweep obeys the operator's kill switch: one behavior, one flag.
// A flag that disabled only the delivery-seam half would no longer
// mean what it says.
func TestDeliverySweepHonorsTheHarvestWakeSwitch(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)

	// A REACHABLE model, deliberately. The first version of this test
	// left the app pointed at an unroutable endpoint, so a wrongly
	// scheduled wake died at the LLM call and the test passed for the
	// wrong reason — its mutation (guard removed) escaped. The fixture
	// must make the forbidden behavior POSSIBLE, or it proves nothing.
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("woke and harvested"))
	}))
	defer srv.Close()
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	off := false
	app.cfgMu.Lock()
	app.cfg.Agency.HarvestWake = &off
	app.cfgMu.Unlock()

	const workerID = "ws_sweepoff"
	queueChild(t, app.store, workerID)
	if err := app.store.StartWorkSession(t.Context(), workerID, store.SubagentDescription("should not wake")); err != nil {
		t.Fatal(err)
	}
	if err := app.store.DeliverWorkSession(t.Context(), workerID, "an outcome nobody asked to be woken for", "", ""); err != nil {
		t.Fatal(err)
	}

	app.sweepDeliveriesAfterTurn()

	// Absence with a stated window: give a wrongly-scheduled wake ample
	// time to take the gate, reach the model, and mark.
	time.Sleep(3 * time.Second)
	if n := len(obs.snapshot()); n != 0 {
		t.Fatalf("the sweep sent %d request(s) with harvest_wake disabled — the switch no longer means what it says", n)
	}
	var harvested int64
	if err := app.store.DB().QueryRow(`SELECT COALESCE(harvested_ms,0) FROM work_sessions WHERE id=?`, workerID).
		Scan(&harvested); err != nil {
		t.Fatal(err)
	}
	if harvested != 0 {
		t.Fatal("a disabled sweep still harvested the outcome")
	}
}
