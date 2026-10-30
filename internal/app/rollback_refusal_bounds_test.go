package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func TestTheRefusalLineBoundIsTakenBackByARollback(t *testing.T) {
	path := writeConfigFile(t)
	keepConfigUnderUpdate(path, Current(), updates.ErrBootRecoveryPending)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if _, err := a.applyConfigChange(map[string]any{"prompt.recent_turns": float64(21)}); err != nil {
		t.Fatal(err)
	}
	if text := readText(t, path); strings.Contains(text, "refusal_") {
		t.Fatalf("a save that did not set it wrote the refusal bound, which 0.1.23 would refuse: %s", text)
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.refusal_lines_per_minute": float64(7)}); err != nil {
		t.Fatal(err)
	}
	newer := readText(t, path)
	if !strings.Contains(newer, `"refusal_lines_per_minute"`) {
		t.Fatalf("the door did not write the key, so this proves nothing: %s", newer)
	}
	if err := putBackConfigKept(path, Current(), true); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != before {
		t.Fatalf("THE ROLLBACK LEFT A KEY 0.1.23 REFUSES: it reads %q, want the bytes it left, %q", got, before)
	}
}
