package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestARevokeOnOnePageIsNotUndoneByAnotherPagesOlderList(t *testing.T) {
	const id = "org.example.uid"
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
	cfg.Tools.CWD = dir
	cfg.Dashboard.Host, cfg.Dashboard.Port, cfg.Dashboard.TLS = "127.0.0.1", 0, false
	cfg.Plugins.Grants = map[string]broker.Grant{id: {KV: true, AutoConfirm: []string{"enroll", "list"}}}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	h := &dashboard.WSHandler{GetConfig: a.configState}
	a.wirePluginHooks(h)
	d, err := a.newDashboard(h)
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	if _, err := d.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	first, second := dialWSS(t, d.LocalURL()), dialWSS(t, d.LocalURL())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	say := func(page *websocket.Conn, requestID, operation string) {
		t.Helper()
		frame, _ := json.Marshal(map[string]any{"type": "plugin", "request_id": requestID,
			"plugin": map[string]string{"action": "revoke", "id": id, "act": operation}})
		if err := page.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
	}
	// until reads the page's frames until one of the type under the
	// request id arrives: the act's answer, or ("") the configuration
	// every page is sent.
	until := func(page *websocket.Conn, typ, requestID, why string) dashboard.ServerMessage {
		t.Helper()
		for {
			_, raw, err := page.Read(ctx)
			if err != nil {
				t.Fatalf("%s: %v", why, err)
			}
			var m dashboard.ServerMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if m.Type == typ && m.RequestID == requestID {
				return m
			}
			if m.Type == "error" && m.RequestID != requestID {
				t.Fatalf("%s: the page was refused under %q: %q", why, m.RequestID, m.Message)
			}
		}
	}
	held := func() []string { return a.configSnapshot().Plugins.Grants[id].AutoConfirm }

	// The first page revokes enroll; both pages are told.
	say(first, "revoke-enroll", "enroll")
	if got := until(first, "config", "revoke-enroll", "the revoking page was not answered under its own id"); got.Config == nil || slices.Contains(held(), "enroll") {
		t.Fatalf("the revoking page was answered, and enroll is still held: %v", held())
	}
	until(second, "config", "", "the other page was not sent the configuration: its card still lists enroll")
	// The second page still showed enroll and list, and revokes list.
	say(second, "revoke-list", "list")
	until(second, "config", "revoke-list", "the second page's revoke was not answered under its id")
	if h := held(); slices.Contains(h, "enroll") {
		t.Fatalf("A REVOKED CONFIRMATION WAS PUT BACK: after one page revoked enroll and another revoked list, %v is confirmed without asking", h)
	}
	onDisk, err := ReadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if g := onDisk.Plugins.Grants[id]; len(g.AutoConfirm) != 0 || !g.KV {
		t.Fatalf("config.json holds %+v, want no standing confirmation and the other grants kept", g)
	}
	// A revoke of what is no longer held is refused under its id, and every
	// page, the refused one among them, is sent the configuration as it is.
	until(first, "config", "", "the second page's revoke was not told to the first")
	say(first, "again", "enroll")
	if got := until(first, "error", "again", "a revoke of what is not held was not refused under its id"); !strings.Contains(got.Message, "nothing was changed") {
		t.Fatalf("a revoke of what is not held was refused with %q", got.Message)
	}
	until(second, "config", "", "after a refused revoke the other page was not sent the configuration as it is")
}

func TestAnAlwaysAndARevokeAtOnceKeepBoth(t *testing.T) {
	const id = "org.example.uid"
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Plugins.Grants = map[string]broker.Grant{id: {KV: true, AutoConfirm: []string{"list"}}}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	held := func() []string { return a.configSnapshot().Plugins.Grants[id].AutoConfirm }
	always := func() error { return a.addAutoConfirm(id, "enroll") }
	revoke := func() error { return a.revokeAutoConfirm(id, "list") }
	reset := func() {
		t.Helper()
		if err := a.revokeAutoConfirm(id, "enroll"); err != nil {
			t.Fatal(err)
		}
		if err := a.addAutoConfirm(id, "list"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(how string, aerr, rerr error) {
		t.Helper()
		if aerr != nil || rerr != nil {
			t.Fatalf("%s: an act was refused: Always %v, revoke %v", how, aerr, rerr)
		}
		if h := held(); !slices.Equal(h, []string{"enroll"}) {
			t.Fatalf("%s: AN ALWAYS AND A REVOKE AT ONCE LOST ONE: %v is confirmed without asking, want [enroll]", how, h)
		}
	}
	for _, first := range []string{"always", "revoke"} {
		a1, a2 := always, revoke
		if first == "revoke" {
			a1, a2 = revoke, always
		}
		e1, e2 := a1(), a2()
		if first == "revoke" {
			e1, e2 = e2, e1
		}
		check(first+" first, one after the other", e1, e2)
		reset()
		for i := 0; i < 100; i++ {
			var wg sync.WaitGroup
			start := make(chan struct{})
			var aerr, rerr error
			wg.Add(2)
			if first == "always" {
				go func() { defer wg.Done(); <-start; aerr = always() }()
				go func() { defer wg.Done(); <-start; rerr = revoke() }()
			} else {
				go func() { defer wg.Done(); <-start; rerr = revoke() }()
				go func() { defer wg.Done(); <-start; aerr = always() }()
			}
			close(start)
			wg.Wait()
			check(first+" started first, at once", aerr, rerr)
			reset()
		}
	}
	// What is already held is refused, and changes nothing.
	if err := a.addAutoConfirm(id, "list"); !errors.Is(err, errStandingHeld) {
		t.Fatalf("an Always for what is already held: %v, want it refused as held", err)
	}
	if h := held(); !slices.Equal(h, []string{"list"}) {
		t.Fatalf("a refused Always changed the list: %v", h)
	}
}
