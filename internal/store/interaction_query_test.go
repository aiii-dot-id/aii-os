package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestInteractionQueryPagesDetailAndAudience(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 125; i++ {
		if _, err = s.RecordInteraction(t.Context(), InteractionInput{ID: fmt.Sprintf("r%d", i), Kind: interaction.Message, Role: interaction.Participant, Content: "same words"}); err != nil {
			t.Fatal(err)
		}
	}
	huge := strings.Repeat("🙂", 20000)
	if _, err = s.RecordInteraction(t.Context(), InteractionInput{ID: "large", Kind: interaction.Message, Role: interaction.Resident, Content: huge}); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOutboxMessage("private", "peer", "friend", "private body", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOutboxNotice("notice", "operator", "", "host notice", nil); err != nil {
		t.Fatal(err)
	}
	q := interaction.Query{Version: 1}
	seen := map[string]bool{}
	var detail *interaction.ContentRef
	for {
		page, err := s.QueryInteractions(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page)
		if len(raw) > interaction.MaxPageBytes {
			t.Fatal("unbounded page", len(raw))
		}
		for _, r := range page.Rows {
			if seen[r.ID] {
				t.Fatal("duplicate cursor occurrence", r.ID)
			}
			seen[r.ID] = true
			if r.Content == "private body" {
				t.Fatal("private body leaked")
			}
			if r.ID == "large" {
				detail = r.ContentRef
			}
			if r.Content == "host notice" && r.Role != interaction.System {
				t.Fatal("notice became resident speech")
			}
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if len(seen) != 127 || detail == nil {
		t.Fatal("missing facts/detail", len(seen), detail)
	}
	req := interaction.ReadRequest{Version: 1, Source: "recorded", ID: detail.ID, Incarnation: detail.Incarnation, SHA256: detail.SHA256, Length: 1024}
	var all []byte
	for {
		r, err := s.ReadInteraction(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, data...)
		req.Offset += int64(r.Bytes)
		if r.EOF {
			break
		}
	}
	var row interaction.Record
	if err = json.Unmarshal(all, &row); err != nil || row.Content != huge {
		t.Fatal("detail changed", err)
	}
	req.Incarnation = "retired"
	if _, err = s.ReadInteraction(t.Context(), req); err == nil {
		t.Fatal("retired source accepted")
	}
	q.Filter.Role = interaction.Operator
	if _, err = s.QueryInteractions(t.Context(), q); err == nil {
		t.Fatal("cursor reused across filters")
	}
}
func TestInteractionSnapshotAnnotationAndSafeRead(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.AddConversationTurn("operator", "hi"); err != nil {
		t.Fatal(err)
	}
	q := interaction.Query{Version: 1, Filter: interaction.Filter{Kinds: []interaction.Kind{interaction.Message}}}
	first, err := s.QueryInteractions(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AnnotateTurn(1, "speaker", "s", `{"speaker":"ContactB"}`); err != nil {
		t.Fatal(err)
	}
	second, err := s.QueryInteractions(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint == first.Fingerprint || len(second.Rows) != 1 || second.Rows[0].Content != "hi" || second.Rows[0].Annotations["speaker"] == nil {
		t.Fatal("annotation did not update coherent view")
	}
	s.SetFrozen(true, "read only")
	third, err := s.QueryInteractions(t.Context(), q)
	if err != nil || third.Fingerprint != second.Fingerprint {
		t.Fatal("SAFE could not read", err)
	}
}

func TestInteractionScrollWindowsShareSnapshotAudienceAndBounds(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 1; i <= 130; i++ {
		_, err = s.RecordInteraction(t.Context(), InteractionInput{ID: fmt.Sprintf("r%d", i), Kind: interaction.Message, Role: interaction.Operator, Content: strings.Repeat("x", 1200)})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Gather a reference through the unchanged public protocol.
	var snapshot []interaction.Record
	legacy := interaction.Query{Version: 1}
	for {
		p, e := s.QueryInteractions(t.Context(), legacy)
		if e != nil {
			t.Fatal(e)
		}
		snapshot = append(snapshot, p.Rows...)
		legacy.Cursor = p.NextCursor
		if legacy.Cursor == "" {
			break
		}
	}
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprint("memory=", memory), func(t *testing.T) {
			inc := s.InteractionIncarnation()
			query := func(q interaction.Query) (*interaction.Page, error) {
				if memory {
					return interaction.MemoryPage(q, inc, append([]interaction.Record(nil), snapshot...), 0)
				}
				return s.QueryInteractions(t.Context(), q)
			}
			read := func(w *interaction.WindowQuery) *interaction.Page {
				p, e := query(interaction.Query{Version: 1, Window: w})
				if e != nil {
					t.Fatal(e)
				}
				if p.Window == nil || p.Window.Fingerprint != p.Fingerprint {
					t.Fatal("unbound window metadata")
				}
				raw, _ := json.Marshal(p)
				if len(raw) > interaction.MaxPageBytes {
					t.Fatal("oversize frame")
				}
				if strings.Contains(string(raw), "has_older") {
					t.Fatal("dashboard metadata changed SDK page wire")
				}
				return p
			}
			latest := read(&interaction.WindowQuery{Mode: "latest"})
			if latest.Rows[len(latest.Rows)-1].ID != "r130" || !latest.Window.HasOlder || latest.Window.HasNewer {
				t.Fatal("bad latest", latest.Window)
			}
			older := read(&interaction.WindowQuery{Mode: "older", Incarnation: inc, AnchorID: latest.Rows[0].ID})
			if older.Rows[len(older.Rows)-1].Sequence+1 != latest.Rows[0].Sequence || !older.Window.HasNewer {
				t.Fatal("older gap")
			}
			newer := read(&interaction.WindowQuery{Mode: "newer", Incarnation: inc, AnchorID: older.Rows[len(older.Rows)-1].ID})
			if newer.Rows[0].ID != latest.Rows[0].ID {
				t.Fatal("newer skipped nearest neighbor")
			}
			q := interaction.Query{Version: 1, Window: &interaction.WindowQuery{Mode: "range", Incarnation: inc, AnchorID: "r10", EndID: "r120"}}
			seen := map[string]bool{}
			pages := 0
			for {
				p, e := query(q)
				if e != nil {
					t.Fatal(e)
				}
				pages++
				for _, r := range p.Rows {
					if seen[r.ID] || r.Sequence < 10 || r.Sequence > 120 {
						t.Fatal("range gap/duplicate/boundary", r.ID)
					}
					seen[r.ID] = true
				}
				q.Cursor = p.NextCursor
				if q.Cursor == "" {
					break
				}
				foreign := q
				foreign.Window = &interaction.WindowQuery{Mode: "range", Incarnation: inc, AnchorID: "r11", EndID: "r120"}
				if _, e = query(foreign); e == nil {
					t.Fatal("cursor reused across ranges")
				}
			}
			if len(seen) != 111 || pages < 3 {
				t.Fatal("incomplete byte-bounded range", len(seen), pages)
			}
			for _, q := range []interaction.Query{
				{Version: 1, Window: &interaction.WindowQuery{Mode: "older", Incarnation: "retired", AnchorID: "r10"}},
				{Version: 1, Window: &interaction.WindowQuery{Mode: "older", Incarnation: inc, AnchorID: "missing"}},
				{Version: 1, Filter: interaction.Filter{Role: interaction.Resident}, Window: &interaction.WindowQuery{Mode: "older", Incarnation: inc, AnchorID: "r10"}},
				{Version: 1, Window: &interaction.WindowQuery{Mode: "range", Incarnation: inc, AnchorID: "r120", EndID: "r10"}},
			} {
				if _, e := query(q); e == nil {
					t.Fatal("invalid/inaccessible window admitted", q)
				}
			}
		})
	}
}

func TestAnInteractionReadWhoseCallerHasGoneIsNotRun(t *testing.T) {
	s := testStore(t)
	if err := s.AddConversationTurn("operator", "hello"); err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.QueryInteractions(gone, interaction.Query{Version: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("a page read ran for a caller that had gone: %v", err)
	}
	if _, _, err := s.OperatorActResults(gone, 0, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("the act results read ran for a caller that had gone: %v", err)
	}
	if _, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1}); err != nil {
		t.Errorf("the same read with its caller present: %v", err)
	}
}

// WHAT THE STORE WRITES, IT READS BACK. A record is written with
// json.Marshal and read with the plugin wire's decoder, which demanded
// canonical JSON the writer never promised: Go writes a speaker score as
// 3.2e-7, and that one row made every history page holding it fail, with
// the stored content blamed on the caller's argument. Paging could not
// step over it, so the history behind it was unreachable.
func TestAStoredRecordNeverBreaksTheHistoryHoldingIt(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.AddConversationTurn("operator", "hi"); err != nil {
		t.Fatal(err)
	}
	if err = s.AnnotateTurn(1, "speaker", "s", `{"speaker":"ContactB","score":3.2e-7}`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordInteraction(t.Context(), InteractionInput{ID: "nul", Kind: interaction.Notice, Role: interaction.System,
		Details: interaction.Details{Reason: "a reason with a \x00 in it"}}); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil {
		t.Fatalf("a stored record broke the history page: %v", err)
	}
	if len(page.Rows) < 2 {
		t.Fatalf("the page lost rows: %+v", page.Rows)
	}
}
