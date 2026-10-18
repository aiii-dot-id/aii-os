package identity

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The reference must come from the normal read interface, not fixture knowledge
// handed straight to the writer. Its header survives a shortened payload.
func TestLedgerRecallReferenceCanBeCitedAndRebuilt(t *testing.T) {
	e, _, lg, kp := citingEngine(t)
	ctx := context.Background()
	const marker = "native-citation-target"
	content := marker + strings.Repeat(" 界Ω", 160) + " END-OF-LONG-PAYLOAD"
	if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": content}); err != nil {
		t.Fatal(err)
	}
	target := lastOfType(t, lg, ledger.EventExperienceCreate)
	read := func(e *Engine) (map[string]any, ledger.Citation) {
		t.Helper()
		before := lg.LastSeq()
		out, err := e.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "ledger", "query": marker})
		if err != nil {
			t.Fatal(err)
		}
		if lg.LastSeq() != before {
			t.Fatal("inspection appended history")
		}
		if !strings.Contains(out, marker) || strings.Contains(out, "END-OF-LONG-PAYLOAD") {
			t.Fatalf("excerpt control failed: %s", out)
		}
		if !strings.Contains(out, "not independent support") {
			t.Fatal("reference was not distinguished from evidence support")
		}
		args, citation := recalledCitation(t, out)
		want := ledger.Citation{Identity: kp.Fingerprint(), Seq: target.Seq, EntryHash: target.EntryHash()}
		if citation != want {
			t.Fatalf("reference=%+v want canonical %+v", citation, want)
		}
		if citation.EntryHash == target.Content {
			t.Fatal("payload hash is not an entry hash")
		}
		return args, citation
	}
	args, original := read(e)
	args["content"] = "This observation names the exact earlier record"
	if _, err := e.ExecuteAction(ctx, "verb", "note", args); err != nil {
		t.Fatalf("copying the recall reference into note: %v", err)
	}
	landed := lastOfType(t, lg, ledger.EventExperienceCreate)
	cites, err := ledger.ParseCitations(landed.Payload)
	if err != nil || len(cites) != 1 || cites[0] != original {
		t.Fatalf("landed citation differs: %v %v", cites, err)
	}
	if _, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), nil); err != nil {
		t.Fatal(err)
	}

	// Real schema-plus-replay opening, then a closed-database restart. The
	// record and its citation remain the same through both ordinary routes.
	path := filepath.Join(t.TempDir(), "rebuilt.db")
	for range 2 {
		rebuilt, err := store.OpenForStartup(ctx, path, store.MirrorHead{Seq: lg.LastSeq(), Hash: lg.LastHash()},
			func(yield func(*ledger.Event) error) error { return ledger.Stream(lg.Path(), yield) })
		if err != nil {
			t.Fatal(err)
		}
		again := NewEngine(rebuilt, &testEventWriter{ledger: lg, store: rebuilt, key: kp}, ring.NewManager(), e.toolDisc, Ports{})
		_, got := read(again)
		if got != original {
			t.Fatal("replay or restart changed the reference")
		}
		if err := rebuilt.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// Rendering a usable reference must not relax existing self-citation
	// admission. The rejected reference cannot append even one record.
	bad, _ := read(e)
	bad["cites"].([]any)[0].(map[string]any)["entry_hash"] = strings.Repeat("f", 64)
	bad["content"] = "A mismatched reference must not land"
	before := lg.LastSeq()
	if _, err := e.ExecuteAction(ctx, "verb", "note", bad); !errors.Is(err, ledger.ErrCitation) {
		t.Fatalf("bad reference was not refused: %v", err)
	}
	if lg.LastSeq() != before {
		t.Fatal("refused citation appended history")
	}
}

func recalledCitation(t *testing.T, out string) (map[string]any, ledger.Citation) {
	t.Helper()
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		if raw, ok := strings.CutPrefix(line, "    cite with "); ok {
			refs = append(refs, raw)
		}
	}
	if len(refs) != 1 {
		t.Fatalf("want one copyable reference, got %d: %s", len(refs), out)
	}
	cites, err := ledger.ParseCitations([]byte(refs[0]))
	if err != nil || len(cites) != 1 {
		t.Fatalf("read reference violates the existing grammar: %v", err)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(refs[0]), &args); err != nil {
		t.Fatal(err)
	}
	return args, cites[0]
}

func TestLedgerRecallDeclaresAnUnavailableReferenceWithoutHidingTheRecord(t *testing.T) {
	for _, damage := range []string{"missing-ring", "missing-fingerprint", "invalid-fingerprint", "unreadable-fingerprint"} {
		t.Run(damage, func(t *testing.T) {
			e, st, lg, _ := citingEngine(t)
			ctx := context.Background()
			const marker = "readable-projection-target"
			if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": marker}); err != nil {
				t.Fatal(err)
			}
			target := lastOfType(t, lg, ledger.EventExperienceCreate)
			// Deliberately damage only this disposable projection. The read
			// must not repair it, hide the row, or supply an invented reference.
			var err error
			switch damage {
			case "missing-ring":
				_, err = st.DB().Exec("UPDATE ledger SET ring=NULL WHERE seq=?", target.Seq)
			case "missing-fingerprint":
				_, err = st.DB().Exec(`UPDATE ledger SET payload='{}' WHERE type='ring0.genesis'`)
			case "invalid-fingerprint":
				_, err = st.DB().Exec(`UPDATE ledger SET payload='{"fingerprint":"not-a-key"}' WHERE type='ring0.genesis'`)
			case "unreadable-fingerprint":
				_, err = st.DB().Exec(`UPDATE ledger SET payload='{"fingerprint":17}' WHERE type='ring0.genesis'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			// total_changes is connection-scoped; keep the diagnostic
			// readback on one connection rather than relying on pool reuse.
			st.DB().SetMaxOpenConns(1)
			before := lg.LastSeq()
			var changes int64
			if err := st.DB().QueryRow("SELECT total_changes()").Scan(&changes); err != nil {
				t.Fatal(err)
			}
			out, err := e.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "ledger", "query": marker})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, marker) || !strings.Contains(out, "citation unavailable:") || !strings.Contains(out, "verify the original ledger") || strings.Contains(out, "cite with ") {
				t.Fatalf("unavailable reference not reported honestly: %s", out)
			}
			if lg.LastSeq() != before {
				t.Fatal("diagnostic read changed the ledger")
			}
			var after int64
			if err := st.DB().QueryRow("SELECT total_changes()").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if changes != after {
				t.Fatal("diagnostic read changed the projection")
			}
		})
	}
}
