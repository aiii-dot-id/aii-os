package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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
func TestQuiesceParksConfigWatcher(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeModel := func(m string) {
		body := fmt.Sprintf(`{"llm":{"provider":"test","model":%q}}`, m)
		if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeModel("m1")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true
	a.watchEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx

	model := func() string {
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		return a.cfg.LLM.Model
	}

	a.SetForeground(false)
	go a.watchConfig(cfgPath)
	time.Sleep(100 * time.Millisecond)

	// .
	writeModel("m2")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(cfgPath, future, future); err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * a.watchEvery)
	if got := model(); got != "m1" {
		t.Fatalf("parked config watcher applied %q — it woke while backgrounded", got)
	}

	a.SetForeground(true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if model() == "m2" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("foreground catch-up never applied the config edit — deferred became lost")
}

// .
// .
// .
// .
// .
func TestQuiesceParksLayoutWatcher(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	a.watchEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	path := a.uiLayoutPath()

	go a.watchUILayout()
	time.Sleep(100 * time.Millisecond)

	a.SetForeground(false)
	time.Sleep(2 * a.watchEvery)
	good := `{"v":1,"profiles":{"desktop":{"panel":["hello"]},"mobile":{"dock":["hello"]}}}`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * a.watchEvery)
	if a.currentUILayout() != nil {
		t.Fatal("parked layout watcher loaded the file — it woke while backgrounded")
	}

	a.SetForeground(true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if string(a.currentUILayout()) == good {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("foreground catch-up never loaded the layout written while parked")
}
