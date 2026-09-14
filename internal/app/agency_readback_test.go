package app

import (
	"path/filepath"
	"testing"
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
// .
// .
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
