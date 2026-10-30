package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// holdRing5AsksOff keeps a waiting start from asking again within a test
// that is about what the start said.
func holdRing5AsksOff(t *testing.T) {
	t.Helper()
	prev := ring5RetryFirst
	t.Cleanup(func() { ring5RetryFirst = prev })
	ring5RetryFirst = time.Hour
}

// ring5Gate answers 503 until it is opened, and then is the test seam's
// Ring 5 server: the platform's bytes, verified by the start as it
// verifies the platform's. It counts what it was asked.
func ring5Gate(t *testing.T) (addr string, open func(), asked *atomic.Int64) {
	t.Helper()
	target, err := url.Parse(genesistest.Ring5Server(t))
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var opened atomic.Bool
	asked = &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if !opened.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() { opened.Store(true) }, asked
}

func TestAStartNoRing5ServerAnsweredIsDegradedAndCompletesWhenOneDoes(t *testing.T) {
	prev := ring5RetryFirst
	t.Cleanup(func() { ring5RetryFirst = prev })
	ring5RetryFirst = 50 * time.Millisecond
	gate, open, asked := ring5Gate(t)
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Waits", nil)
	cfg := safebootConfig(t, dir, "Waits", keyPath, ledgerPath, dbPath)
	cfg.Genesis.FirewallURL, cfg.Genesis.FetchAttempts, cfg.Genesis.Ring5RetrySeconds = gate, 1, 1
	before := recordAndDatabase(t, cfg)
	a := New(cfg)
	if err := a.startLive(); err != nil {
		t.Fatalf("a start no Ring 5 server answered must hold its page: %v", err)
	}
	defer stopApp(t, a)
	reason, held := a.SafeMode()
	if !held || a.live.Load() {
		t.Fatal("the start went on without Ring 5")
	}
	trigger, _, _ := a.safeCause()
	if trigger != safeRing5Unreached || !strings.HasPrefix(reason, ring5WaitWhat+": ") || !strings.Contains(reason, "returned 503") ||
		!strings.Contains(reason, "asks for it again by itself") || !strings.HasSuffix(reason, safePostures[safeRing5Unreached].route+".") {
		t.Fatalf("the reason is not a start waiting for Ring 5 (trigger %d): %s", trigger, reason)
	}
	c, err := a.continuityState()
	if err != nil || c.Mode != "degraded_ring5" || c.DegradedWhy != reason || c.SafeReason != "" {
		t.Fatalf("the strip does not say DEGRADED: mode=%q why=%q safe=%q err=%v", c.Mode, c.DegradedWhy, c.SafeReason, err)
	}
	// No beacon calls for the operator: the start completes by itself.
	a.mode.mu.RLock()
	beacon := a.mode.beaconStop
	a.mode.mu.RUnlock()
	if beacon != nil {
		t.Fatal("a start that waits for Ring 5 calls for the operator with SAFE's beacon")
	}
	ring0 := a.rings.GetContent(ring.Ring0)
	if !strings.HasPrefix(ring0, "# DEGRADED") || !strings.Contains(ring0, ring5WaitWhy) || strings.Contains(ring0, "SAFE MODE") || strings.Contains(ring0, safeOperatorKnows) {
		t.Fatalf("the resident is not told the start is DEGRADED and waiting:\n%s", ring0)
	}
	// The request opens as the posture it is: DEGRADED, not SAFE, whose
	// opening says the operator must act and points at a SAFE MODE section.
	p, err := a.composeTurn(a.readSafe(), "", "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, prompt.DegradedOpening) || strings.Contains(p.Text, prompt.SafeOpening) || strings.Contains(p.Text, "SAFE MODE") {
		t.Fatalf("a start that waits does not open as DEGRADED:\n%.600s", p.Text)
	}
	if fileExists(filepath.Join(dir, ".boot_completed")) {
		t.Fatal("a start that is waiting wrote the boot marker: it has not completed")
	}
	// It keeps asking while no server answers.
	deadline := time.Now().Add(20 * time.Second)
	for asked.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if asked.Load() < 3 {
		t.Fatalf("the start did not ask again: %d requests", asked.Load())
	}
	if a.restarting.Load() {
		t.Fatal("the start was made again while no server answered")
	}
	// A server that answers ends the wait: the start is made again.
	open()
	for !a.restarting.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.restarting.Load() {
		t.Fatal("A START WAITING FOR RING 5 WAS NOT MADE AGAIN WHEN A SERVER ANSWERED: it would wait for a person")
	}
	// Nothing was written to the record or the database while it waited.
	stopApp(t, a)
	if after := recordAndDatabase(t, cfg); after != before {
		t.Fatalf("A START THAT WAITED FOR RING 5 CHANGED THE RECORD OR THE DATABASE: %v, then %v", before, after)
	}
}

// A RING 5 THAT A SERVER SENT AND THE CHECKS REFUSED IS AN ANSWER: SAFE, as
// it was, and not asked again.
func TestAStartWhoseRing5WasRefusedIsSafeAndDoesNotAskAgain(t *testing.T) {
	prev := ring5RetryFirst
	t.Cleanup(func() { ring5RetryFirst = prev })
	ring5RetryFirst = 50 * time.Millisecond
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_, _ = w.Write([]byte("not a bundle"))
	}))
	defer srv.Close()
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "Refused", nil)
	cfg := safebootConfig(t, dir, "Refused", keyPath, ledgerPath, dbPath)
	cfg.Genesis.FirewallURL, cfg.Genesis.FetchAttempts, cfg.Genesis.Ring5RetrySeconds = srv.URL, 1, 1
	a := New(cfg)
	if err := a.startLive(); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	reason, safe := a.SafeMode()
	trigger, _, _ := a.safeCause()
	if !safe || trigger != safeRing5 || !strings.HasPrefix(reason, ring5What+": ") {
		t.Fatalf("a refused Ring 5 is not SAFE's (trigger %d): %s", trigger, reason)
	}
	if c, err := a.continuityState(); err != nil || c.Mode != "safe" {
		t.Fatalf("the strip: mode=%q err=%v", c.Mode, err)
	}
	before := asked.Load()
	time.Sleep(500 * time.Millisecond)
	if after := asked.Load(); after != before || a.restarting.Load() {
		t.Fatalf("a refusal was asked again (%d requests, then %d) or the start made again (%v)", before, after, a.restarting.Load())
	}
}

func TestOnlyARing5ServerThatDoesNotAnswerIsWaitedFor(t *testing.T) {
	fault := &tlsfault.Fault{Host: "genesis.example", Reason: tlsfault.Expired, Err: errors.New("x509: certificate has expired")}
	for _, tc := range []struct {
		name      string
		err       error
		waits     bool
		certified bool
	}{
		{"no answer", &genesis.NotAnsweredError{Err: errors.New("dial tcp: connection refused")}, true, false},
		{"only 5xx", &genesis.NotAnsweredError{Err: errors.New("returned 503")}, true, false},
		{"a refused certificate", &genesis.NotAnsweredError{Err: fault}, false, true},
		{"a refused certificate, then no answer", &genesis.NotAnsweredError{Err: fmt.Errorf("%w; before it, %w", errors.New("i/o timeout"), fault)}, false, true},
		{"an answer the checks refused", errors.New("ring5 bundle does not verify"), false, false},
	} {
		if got := notAnswered(tc.err); got != tc.waits {
			t.Errorf("%s: waited for = %v", tc.name, got)
		}
		if got := refusedCertificate(tc.err); got != tc.certified {
			t.Errorf("%s: a refused certificate = %v", tc.name, got)
		}
	}
	if p := safePostures[safeRing5Refused]; p.degraded || p.unhealthy || !p.passedBefore || p.what != ring5What || p.why != ring5Why {
		t.Fatalf("a refused certificate's posture is not a Ring 5 SAFE's: %+v", p)
	}
}

// WHILE AN UPDATE'S ROLLBACK IS ARMED, A START NO RING 5 SERVER ANSWERED
// ENDS: the previous program reached the network to fetch this one, so a
// program that cannot is as likely the update's fault, and the next start
// restores the previous program. A page that waited would never be
// restarted, and the update would never be rolled back.
func TestAStartNoRing5ServerAnsweredEndsWhileAnUpdatesRollbackIsArmed(t *testing.T) {
	whereAnEndedStartIsStartedAgain(t)
	holdRing5AsksOff(t)
	gate, _, _ := ring5Gate(t)
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "UpdatedDark", nil)
	backup := []byte("the previous binary")
	sum := sha256.Sum256(backup)
	if err := os.WriteFile(filepath.Join(dir, "aii.previous"), backup, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"attempts":0,"backup_sha256":"` + hex.EncodeToString(sum[:]) + `","new_sha256":"` + strings.Repeat("0", 64) + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".update_pending"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := safebootConfig(t, dir, "UpdatedDark", keyPath, ledgerPath, dbPath)
	cfg.Genesis.FirewallURL, cfg.Genesis.FetchAttempts = gate, 1
	a := New(cfg)
	err := a.startLive()
	_, held := a.SafeMode()
	stopApp(t, a)
	if err == nil || held {
		t.Fatalf("A START WAITING FOR RING 5 HELD A PAGE WHILE AN UPDATE'S ROLLBACK WAS ARMED (err %v, page %v)", err, held)
	}
	if !strings.Contains(err.Error(), "fetch Ring 5: ") || !updates.RollbackArmed(dir) {
		t.Fatalf("the start must end on the fetch, with the rollback still armed: armed=%v err=%v", updates.RollbackArmed(dir), err)
	}
}
