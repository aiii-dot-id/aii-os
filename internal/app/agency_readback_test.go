package app

import (
	"path/filepath"
	"testing"
)

// THE CHECKBOX must come back. The Settings page paints agency.prefer_local_for_roles
// from the readback and waits for the echo to match what it sent, so a
// ConfigState that omits the field renders the box UNCHECKED after a save the
// server accepted — and the save banner then waits forever for an echo that
// cannot arrive. The field is therefore load-bearing in both directions, which
// is what this pins: on, off, and back.
//
// It also pins that the checkbox is LIVE. llmForRole reads the config snapshot
// per spawn, so the next role-tagged spawn honours the new answer; a
// restart_required entry here would be the UI telling the operator to reboot
// for a change that already took effect.
func TestAgencyPreferLocalForRolesRoundTripsThroughTheReadback(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	a := New(cfg)

	if a.configState().Agency.PreferLocalForRoles {
		t.Fatal("the shipped state is off; a readback that starts true proves nothing below")
	}

	st, err := a.applyConfigChange(map[string]interface{}{"agency.prefer_local_for_roles": true})
	if err != nil {
		t.Fatalf("checking the box: %v", err)
	}
	if !st.Agency.PreferLocalForRoles {
		t.Error("the box was checked and the readback says unchecked — the Settings page repaints it off and its save banner never matches")
	}
	if len(st.RestartRequired) != 0 {
		t.Errorf("the checkbox applies live (llmForRole reads the snapshot per spawn), restart_required = %v", st.RestartRequired)
	}

	off, err := a.applyConfigChange(map[string]interface{}{"agency.prefer_local_for_roles": false})
	if err != nil {
		t.Fatalf("unchecking the box: %v", err)
	}
	if off.Agency.PreferLocalForRoles {
		t.Error("unchecked, and the readback still says checked — the operator cannot turn local routing back off from the page")
	}
	if len(off.RestartRequired) != 0 {
		t.Errorf("unchecking applies live too, restart_required = %v", off.RestartRequired)
	}
}
