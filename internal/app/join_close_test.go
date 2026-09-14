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

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestDeliveryDuringATurnIsHarvestedAfterItEnds(t *testing.T) {
	app, dir := dispatchApp(t)
	defer app.Stop()

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "join probe"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	const workerID = "ws_joinprobe"
	landed := make(chan struct{})

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1:
			// .
			// .
			// .
			if err := app.store.StartWorkSession(workerID, store.SubagentDescription("count the beans")); err != nil {
				t.Error(err)
			}
			if err := app.store.DeliverWorkSession(workerID, "beans counted: 41", "", ""); err != nil {
				t.Error(err)
			}
			close(landed)
			_, _ = io.WriteString(w, contToolResp(contCall("c1", "read", string(readArgs))))
		case n == 2:
			_, _ = io.WriteString(w, contTextResp("parent turn done"))
		default:
			// .
			_, _ = io.WriteString(w, contTextResp("harvested the outcome"))
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

	// .
	// .
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

// .
// .
// .
func TestDeliverySweepHonorsTheHarvestWakeSwitch(t *testing.T) {
	app, _ := dispatchApp(t)
	defer app.Stop()

	// .
	// .
	// .
	// .
	// .
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, contTextResp("woke and harvested"))
	}))
	defer srv.Close()
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	off := false
	app.cfgMu.Lock()
	app.cfg.Agency.HarvestWake = &off
	app.cfgMu.Unlock()

	const workerID = "ws_sweepoff"
	if err := app.store.StartWorkSession(workerID, store.SubagentDescription("should not wake")); err != nil {
		t.Fatal(err)
	}
	if err := app.store.DeliverWorkSession(workerID, "an outcome nobody asked to be woken for", "", ""); err != nil {
		t.Fatal(err)
	}

	app.sweepDeliveriesAfterTurn()

	// .
	// .
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
