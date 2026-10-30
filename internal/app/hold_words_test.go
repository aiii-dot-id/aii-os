package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// heldWords is what a held start's refusals say, one per door a resident or
// an operator meets in the read-only posture, with the runtime's own reason
// cut out so that only the refusal's own words are compared.
func heldWords(t *testing.T, a *App) map[string]string {
	t.Helper()
	reason, held := a.SafeMode()
	if !held {
		t.Fatal("rig: the start is not held")
	}
	cut := func(s string) string { return strings.ReplaceAll(s, reason, "<reason>") }
	errText := func(err error) string {
		if err == nil {
			return "<no refusal>"
		}
		return cut(err.Error())
	}
	said := map[string]string{}
	res, _ := a.toolReg.Execute(context.Background(), "write", map[string]any{"file_path": "x.txt", "content": "y"})
	said["tool"] = cut(res.Error)
	_, err := a.engine.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "a thought"})
	said["note"] = errText(err)
	_, err = a.engine.ExecuteAction(context.Background(), "verb", "commit", map[string]any{"variant": "belief", "content": "x"})
	said["commit"] = errText(err)
	_, err = a.engine.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "project.create", "name": "p"})
	said["project"] = errText(err)
	var frozen *store.FrozenError
	if err := a.store.InsertTurnMetric(store.TurnMetric{TsMs: 1, Calls: 1, Rounds: 1}); errors.As(err, &frozen) {
		said["store"] = cut(frozen.Error())
	} else {
		said["store"] = errText(err)
	}
	if a.ledger != nil {
		_, err = a.ledger.Append(ledger.EventExperienceCreate, 1, map[string]any{"id": "exp_x", "content": "x"}, a.keyPair)
		said["ledger"] = errText(err)
	}
	_, err = voiceModeAdapter{a}.SetMode(listenInteractive, "")
	said["voice"] = errText(err)
	_, said["sandbox"] = a.sandboxHeld(a.configSnapshot())
	said["sandbox door"] = errText(a.sandboxHeldInPosture())
	said["outbox"] = a.waitingLine(store.OutboxMessage{})
	said["outbox note"] = waitingNote(a.waitingFor(store.OutboxMessage{}), "")
	var parts []string
	_ = a.diagnosticPromptRead(&parts, "work", errors.New("gone"))
	said["prompt"] = strings.Join(parts, "")
	said["meaning"] = a.whileHeld()
	_, err = a.continuity.Port().Take(context.Background())
	said["continuity"] = errText(err)
	return said
}

func TestARefusalUnderAStartThatWaitsDoesNotSaySafe(t *testing.T) {
	holdRing5AsksOff(t)
	gate, _, _ := ring5Gate(t)
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "WaitsWords", nil)
	cfg := safebootConfig(t, dir, "WaitsWords", keyPath, ledgerPath, dbPath)
	cfg.Genesis.FirewallURL, cfg.Genesis.FetchAttempts = gate, 1
	a := New(cfg)
	if err := a.startLive(); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if h, held := a.Hold(); !held || !h.Waiting {
		t.Fatalf("rig: the start does not wait (held %v, %+v)", held, h)
	}
	for door, text := range heldWords(t, a) {
		if text == "" || text == "<no refusal>" {
			t.Errorf("%s: nothing was refused, so nothing is shown: %q", door, text)
			continue
		}
		for _, never := range []string{"SAFE", "safe mode", "Safe mode"} {
			if strings.Contains(text, never) {
				t.Errorf("A REFUSAL UNDER A START THAT WAITS SAYS %q (%s): %s", never, door, text)
			}
		}
	}
}

// AND UNDER SAFE, NOTHING CHANGED: each refusal is the text it was.
func TestARefusalUnderSafeIsTheTextItWas(t *testing.T) {
	dir, keyPath, ledgerPath, dbPath := recordedBy(t, "SafeWords", nil)
	cfg := safebootConfig(t, dir, "SafeWords", keyPath, ledgerPath, dbPath)
	cfg.Genesis.FirewallURL = ""
	a := New(cfg)
	if err := a.startLive(); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if h, held := a.Hold(); !held || h.Waiting {
		t.Fatalf("rig: the start is not SAFE (held %v, %+v)", held, h)
	}
	said := heldWords(t, a)
	for door, want := range map[string]string{
		"tool":         "refused: I am in safe mode — <reason>. Only read-only tools continue while SAFE holds.",
		"note":         "note refused: I am in safe mode — <reason>. Nothing is written to my record while SAFE holds.",
		"store":        "the projection is frozen: <reason> (SAFE: it is queryable, not writable)",
		"voice":        "the mode cannot change while this identity is in SAFE (<reason>): your operator clears SAFE, and the mode is yours again",
		"sandbox":      "SAFE holds, and while it does the sandbox is on and cannot be turned off.",
		"sandbox door": "the sandbox stays on: SAFE holds, and while it does the sandbox is on whatever the saved setting says; the saved setting applies again at a start that passes",
		"outbox":       "- to : not sent — nothing leaves in safe mode; it stays queued until safe mode ends",
		"outbox note":  "Held: nothing leaves in safe mode. It is sent once safe mode ends.",
		"meaning":      "while the identity is in SAFE",
	} {
		if said[door] != want {
			t.Errorf("%s under SAFE changed:\n got %q\nwant %q", door, said[door], want)
		}
	}
	for _, door := range []string{"commit", "project", "ledger", "prompt", "continuity"} {
		if !strings.Contains(said[door], "SAFE") && !strings.Contains(said[door], "safe mode") {
			t.Errorf("%s under SAFE no longer says SAFE: %q", door, said[door])
		}
	}
}
