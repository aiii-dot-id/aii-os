package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestPluginMemoryExactIntakeSerializesConcurrentHostCalls(t *testing.T) {
	s := testStore(t)
	scope := PluginScope{PluginID: "fixture"}
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := s.PluginMemoryAddExact(scope, PluginMemory{ID: fmt.Sprintf("candidate-%d", i), Text: "same validated method", Applicability: "task", WorkSession: "w", Project: "p", Evidence: "[]"}, 1)
			ids <- id
			errs <- err
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 || unique[""] {
		t.Fatal("concurrent calls created duplicate equivalents", unique)
	}
	if n, err := s.PluginMemoryCount(scope); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestPluginMemoryCorrectionIsAtomicAndScopeBound(t *testing.T) {
	s := testStore(t)
	scope := PluginScope{PluginID: "fixture"}
	base := PluginMemory{ID: "original", Text: "check both results", Applicability: "task", Project: "p", WorkSession: "w", Evidence: "[]"}
	if err := s.PluginMemoryAdd(scope, base, 10, ""); err != nil {
		t.Fatal(err)
	}
	wrong := base
	wrong.ID = "wrong"
	wrong.WorkSession = "another"
	if err := s.PluginMemoryAdd(scope, wrong, 10, base.ID); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatal("scope correction accepted", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"first", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			next := base
			next.ID = id
			next.Text = "correction " + id
			results <- s.PluginMemoryAdd(scope, next, 10, base.ID)
		}(id)
	}
	wg.Wait()
	close(results)
	passed, rejected := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, ErrPluginMemoryNotFound) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || rejected != 1 {
		t.Fatal("concurrent correction overwrote", passed, rejected)
	}
	if count, err := s.PluginMemoryCount(scope); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestPluginMemoryReferencesSurviveRestartAndDiscloseLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordInteraction(t.Context(), InteractionInput{ID: "evidence", Kind: interaction.Message, Role: interaction.Participant, Content: "synthetic observed output"}); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil || len(page.Rows) != 1 {
		t.Fatal(page, err)
	}
	r := page.Rows[0]
	raw, _ := json.Marshal(r)
	ref := MemoryEvidenceRef{ID: r.ID, Sequence: strconv.FormatUint(r.Sequence, 10), SHA256: interaction.Hash(raw), Incarnation: page.Incarnation}
	evidence, _ := json.Marshal([]MemoryEvidenceRef{ref})
	scope := PluginScope{PluginID: "fixture"}
	if err = s.PluginMemoryAdd(scope, PluginMemory{ID: "lesson", Text: "observed method", Applicability: "reusable", WorkSession: "source-task", SourceIncarnation: ref.Incarnation, Evidence: string(evidence)}, 10, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.InteractionIncarnation() == ref.Incarnation {
		t.Fatal("fixture did not reopen a new source instance")
	}
	if status, err := s.PluginMemoryEvidenceStatus(t.Context(), ref); err != nil || status != "available" {
		t.Fatal("same retained row lost after restart", status, err)
	}
	m, found, err := s.PluginMemoryGet(scope, "lesson")
	if err != nil || !found || m.SourceIncarnation != ref.Incarnation || m.Evidence != string(evidence) {
		t.Fatal("provenance rewritten on reopen", m, err)
	}
	changed := ref
	changed.Sequence = "9999"
	if status, err := s.PluginMemoryEvidenceStatus(t.Context(), changed); err != nil || status != "changed" {
		t.Fatal(status, err)
	}
	if _, err = s.db.Exec("DELETE FROM conversations WHERE id = ?", ref.ID); err != nil {
		t.Fatal(err)
	}
	if status, err := s.PluginMemoryEvidenceStatus(t.Context(), ref); err != nil || status != "unavailable" {
		t.Fatal("absent evidence became valid", status, err)
	}
}
