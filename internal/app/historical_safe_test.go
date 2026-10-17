package app

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoricalSafeConversationSurvivesUnavailableViewsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	key, record, _ := birthFixture(t, dir, "HistoricalSafe")
	path := filepath.Join(dir, "historical.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := os.ReadFile("../store/testdata/schema-pre-work-queue.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO work_sessions(id,description,state) VALUES('main','historical task','retained working state')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	tamperChain(t, key, record)
	_, provider := newScriptedToolLLM(t, "HISTORICALSAFE", nil, "Diagnostic conversation remains available.", -1)
	cfg := safebootConfig(t, dir, "HistoricalSafe", key, record, path)
	cfg.LLM = withTestProvider(t, dir, "test", provider.URL, "m", "sk-test")
	a := New(cfg)
	defer stopApp(t, a)
	before, _ := fileDigest(t, path)
	ledgerBefore, _ := fileDigest(t, record)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("fixture did not reach actual boot-SAFE")
	}
	state, err := a.buildWorkState()
	if err != nil || !strings.Contains(state, "retained working state") {
		t.Errorf("recorded work state was lost: %q %v", state, err)
	}
	facts, err := a.buildTurnFacts(t.Context(), true)
	if err != nil || !strings.Contains(facts, "delivered work") || !strings.Contains(facts, "unavailable") {
		t.Errorf("unavailable historical sections were not disclosed: %q %v", facts, err)
	}
	reply, err := a.handleMessage(t.Context(), "HISTORICALSAFE help me inspect the problem")
	if err != nil || !strings.Contains(reply, "Diagnostic conversation remains available") {
		t.Fatalf("historical SAFE conversation failed: %q %v", reply, err)
	}
	if len(a.engine.SafeTranscript()) < 2 {
		t.Fatal("diagnostic conversation did not retain its transient transcript")
	}
	stopApp(t, a)
	if after, _ := fileDigest(t, path); before != after {
		t.Fatal("SAFE diagnostic conversation changed the original database")
	}
	if after, _ := fileDigest(t, record); ledgerBefore != after {
		t.Fatal("SAFE diagnostic conversation changed the rejected record")
	}
}

func TestDiagnosticPromptReadKeepsNormalFailureAndNamesSafeOmission(t *testing.T) {
	a := &App{}
	fault := errors.New("synthetic unreadable section")
	var parts []string
	if err := a.diagnosticPromptRead(&parts, "delivered work", fault); !errors.Is(err, fault) || len(parts) != 0 {
		t.Fatalf("normal mode swallowed a failed read: %v %v", parts, err)
	}
	a.mode.mode = ModeSafe // synchronous fixture; no background or durable state
	if err := a.diagnosticPromptRead(&parts, "delivered work", fault); err != nil || len(parts) != 1 || !strings.Contains(parts[0], "unavailable") || !strings.Contains(parts[0], "runtime log") {
		t.Fatalf("SAFE read failed silently or lost its diagnostic route: %v %v", parts, err)
	}
}
