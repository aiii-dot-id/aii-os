package identity

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestConversationRecordingLocationNamesTheActualOwner(t *testing.T) {
	for _, mode := range []string{"normal", "safe", "failed"} {
		t.Run(mode, func(t *testing.T) {
			st, err := store.NewMemory()
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			e := NewEngine(st, nil, nil, nil, Ports{})
			if mode == "safe" {
				e.SetSafeMode("fixture")
			}
			if mode == "failed" {
				st.SetFrozen(true, "fixture")
			}
			ref, loc, err := e.RecordConversationLocated(context.Background(), "operator", "same words", interaction.Details{})
			if (err != nil) != (mode == "failed") {
				t.Fatalf("wrong write result: %v", err)
			}
			if ref.ID == "" || ref.ID != loc.ID || loc.Incarnation == "" {
				t.Fatalf("lost occurrence: %+v %+v", ref, loc)
			}
			if mode == "normal" {
				p, err := st.QueryInteractions(t.Context(), interaction.Query{Version: 1})
				if err != nil || len(p.Rows) != 1 || p.Rows[0].ID != loc.ID || p.Incarnation != loc.Incarnation || loc.Source != "recorded" || ref.Sequence == 0 {
					t.Fatalf("not durable: %+v %+v %v", loc, p, err)
				}
			} else {
				inc, rows, _ := e.TransientInteractions()
				if len(rows) != 1 || rows[0].ID != loc.ID || inc != loc.Incarnation || loc.Source != "transient" || ref.Sequence != 0 {
					t.Fatalf("not transient: %+v %+v", loc, rows)
				}
				if (rows[0].Details.RecordingError != "") != (mode == "failed") {
					t.Fatal("failure lost its qualification")
				}
			}
		})
	}
}

func TestRecordingLocationSurvivesImmediateTransientEviction(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := NewEngine(st, nil, nil, nil, Ports{})
	st.SetFrozen(true, "fixture")
	var original interaction.Location
	e.SetInteractionObserver(func() {
		e.SetInteractionObserver(nil)
		inc, rows, _ := e.TransientInteractions()
		original = interaction.Location{Source: "transient", Incarnation: inc, ID: rows[0].ID}
		e.SetSafeMode("fixture")
		for i := 0; i < 201; i++ {
			if _, err := e.RecordConversationRef(context.Background(), "operator", "same words", interaction.Details{}); err != nil {
				t.Fatal(err)
			}
		}
	})
	_, loc, err := e.RecordConversationLocated(context.Background(), "operator", "same words", interaction.Details{})
	if err == nil || loc != original {
		t.Fatalf("reconstructed receipt from later owner state: %+v want %+v (%v)", loc, original, err)
	}
	_, rows, lost := e.TransientInteractions()
	if lost == 0 || rows[0].ID == loc.ID {
		t.Fatal("test did not evict the original record")
	}
}
