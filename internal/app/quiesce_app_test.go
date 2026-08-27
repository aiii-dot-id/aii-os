package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The quiesce governor at the app seam (2026-08-19, the battery fix):
// SetForeground(false) parks the periodic watchers — no ticks, no file
// stats, no CPU wakeups — and SetForeground(true) resumes them with one
// immediate catch-up pass, so an edit made while backgrounded applies
// the moment the operator returns. watchEvery is the sessionGrace-style
// in-package test hook.

// Config watcher, born parked: backgrounded before the watcher starts,
// an operator edit is NOT applied across several poll intervals;
// foreground applies it promptly (the catch-up tick).
func TestQuiesceParksConfigWatcher(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeModel := func(m string) {
		body := fmt.Sprintf(`{"llm":{"provider":"test","model":%q}}`, m) // the pointer shape (2026-08-20)
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
	a.live = true // reloadConfig's gate: only LIVE apps apply reloads
	a.watchEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx

	model := func() string {
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		return a.cfg.LLM.Model
	}

	a.SetForeground(false) // background FIRST: the watcher must be born parked
	go a.watchConfig(cfgPath)
	time.Sleep(100 * time.Millisecond) // the watcher's baseline stat sees m1

	// The operator edits config while the app is backgrounded.
	writeModel("m2")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(cfgPath, future, future); err != nil { // defeat coarse-mtime filesystems
		t.Fatal(err)
	}

	time.Sleep(5 * a.watchEvery) // ≥3 intervals of required silence
	if got := model(); got != "m1" {
		t.Fatalf("parked config watcher applied %q — it woke while backgrounded", got)
	}

	a.SetForeground(true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if model() == "m2" {
			return // the catch-up tick applied the deferred edit
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("foreground catch-up never applied the config edit — deferred became lost")
}

// Layout watcher, parked mid-run: the running watcher pauses on
// background (its ticker STOPS), stays silent across several intervals,
// and the foreground catch-up tick picks up the write that landed while
// parked. Harness shape follows TestUILayoutLoadValidateWatch.
func TestQuiesceParksLayoutWatcher(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	a.watchEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	path := a.uiLayoutPath()

	go a.watchUILayout()
	time.Sleep(100 * time.Millisecond) // baseline stat: absent file, watcher RUNNING

	a.SetForeground(false)
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
