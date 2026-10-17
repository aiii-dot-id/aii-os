package app

import "testing"

// A successor may start and receive steering before the predecessor's
// release call returns. Step that schedule at the actual token handoff:
// only the predecessor's undrained words may become its leftover turn.
func TestTurnHandoffKeepsEachTurnsSteering(t *testing.T) {
	for _, oldSteer := range []bool{false, true} {
		name := "empty predecessor queue"
		if oldSteer {
			name = "undrained predecessor queue"
		}
		t.Run(name, func(t *testing.T) {
			a := gateApp()
			if !a.TryBeginTurn() {
				t.Fatal("fixture could not take the first turn")
			}
			var wantOld []string
			if oldSteer {
				if steered, err := a.Steer("previous turn's unread words"); !steered || err != nil {
					t.Fatalf("previous steer: steered=%v err=%v", steered, err)
				}
				wantOld = append(wantOld, "previous turn's unread words")
			}
			// Cleanup happens before handoff and outside turnMu. An arrival
			// here must not fall into a gap after the last queue collection.
			a.holdVoice(&voiceBinding{done: func() {
				if steered, err := a.Steer("words arriving during cleanup"); !steered || err != nil {
					t.Fatalf("cleanup steer: steered=%v err=%v", steered, err)
				}
			}})
			wantOld = append(wantOld, "words arriving during cleanup")
			var flushed []steerEntry
			a.steerFlush = func(entries []steerEntry) { flushed = append(flushed, entries...) }
			a.turnAfterHandoff = func() {
				if steered, err := a.admitOperatorChat(t.Context(), "the successor opens"); steered || err != nil {
					t.Fatalf("successor admission: steered=%v err=%v", steered, err)
				}
				if steered, err := a.admitOperatorChat(t.Context(), "the successor's own correction"); !steered || err != nil {
					t.Fatalf("successor steer: steered=%v err=%v", steered, err)
				}
			}
			a.EndTurn()
			a.turnAfterHandoff = nil
			defer a.EndTurn()
			if len(flushed) != len(wantOld) {
				t.Errorf("predecessor flushed %d words, want only its %d: %+v", len(flushed), len(wantOld), flushed)
			} else {
				for i, want := range wantOld {
					if flushed[i].content != want {
						t.Errorf("predecessor word %d=%q, want %q", i, flushed[i].content, want)
					}
				}
			}
			if got := a.DrainSteering(); len(got) != 1 || got[0] != "the successor's own correction" {
				t.Errorf("predecessor took the successor's steering: %v", got)
			}
		})
	}
}

// Step the actual admission path at the stale-snapshot window, without a
// sleep or scheduler lottery. The releasing turn drains an empty queue before
// this message can append. It must become the next ordinary turn, not be
// acknowledged as steered with nobody left to read it.
func TestAdmissionCannotStrandASteerBehindACompletedTurn(t *testing.T) {
	a := gateApp()
	if !a.TryBeginTurn() {
		t.Fatal("fixture could not open the first turn")
	}
	t.Cleanup(func() {
		a.steerBeforeQueue = nil
		if a.TurnActive() {
			a.EndTurn()
		}
	})
	var flushed []steerEntry
	a.steerFlush = func(entries []steerEntry) { flushed = append(flushed, entries...) }
	stepped := false
	a.steerBeforeQueue = func() {
		if stepped {
			t.Fatal("admission repeated the release step")
		}
		stepped = true
		a.EndTurn()
	}
	steered, err := a.admitOperatorChat(t.Context(), "the message arriving as the previous turn ends")
	if !stepped {
		t.Fatal("the release/admission boundary was not exercised")
	}
	if err != nil || steered || !a.TurnActive() || len(a.PendingSteers()) != 0 || len(flushed) != 0 {
		t.Fatalf("message must own the next ordinary turn: steered=%v active=%v pending=%v flushed=%d err=%v", steered, a.TurnActive(), a.PendingSteers(), len(flushed), err)
	}
}
