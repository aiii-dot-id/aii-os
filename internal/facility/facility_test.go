package facility

import "testing"

func TestSetMembershipAndSnapshot(t *testing.T) {
	present := false
	s, err := NewSet(
		Facility{Name: TransportLocal, Provider: "bbb-stdio (linux)"},
		Facility{Name: OperatorPresenceFresh, Provider: "dashboard-session (linux)", Live: func() bool { return present }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Has(TransportLocal) || !s.Has(OperatorPresenceFresh) {
		t.Fatal("advertised facilities must be members")
	}
	if s.Has(ForegroundLifecycle) || s.Has("sev_audio.raw_pcm") {
		t.Fatal("unadvertised facilities must not be members — an empty answer is the honest one")
	}

	// Status is telemetry, membership is not: a momentarily-idle
	// presence facility stays a member.
	snap := s.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot rows = %d, want 2", len(snap))
	}
	for _, row := range snap {
		switch row.Name {
		case TransportLocal:
			if !row.Live || row.Provider == "" {
				t.Fatalf("structurally-live facility must report live with its provider: %+v", row)
			}
		case OperatorPresenceFresh:
			if row.Live {
				t.Fatalf("idle presence must report not-live: %+v", row)
			}
		}
	}
	present = true
	for _, row := range s.Snapshot() {
		if row.Name == OperatorPresenceFresh && !row.Live {
			t.Fatal("presence status must track the probe")
		}
	}
	if !s.Has(OperatorPresenceFresh) {
		t.Fatal("membership must not depend on momentary status")
	}
}

func TestSetRefusesWiringDefects(t *testing.T) {
	if _, err := NewSet(Facility{Name: ""}); err == nil {
		t.Fatal("an unnamed facility must refuse")
	}
	if _, err := NewSet(Facility{Name: TransportLocal}, Facility{Name: TransportLocal}); err == nil {
		t.Fatal("a duplicate advertisement must refuse")
	}
}

func TestNilSetAdvertisesNothing(t *testing.T) {
	var s *Set
	if s.Has(TransportLocal) {
		t.Fatal("a nil set must advertise nothing (fail-closed)")
	}
	if s.Names() != nil || s.Snapshot() != nil {
		t.Fatal("a nil set has no names and no snapshot")
	}
}
