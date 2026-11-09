package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FIRST RUN'S "COPY THOSE NOTES" IS SAVED BEFORE A BIRTH, AND ONLY THEN
// (spec step 7). The page saves genesis.agent_memory.off through the
// settings door before it asks for a birth: the door takes either value,
// keeps it for the birth to read, and reads it back as the page shows it;
// the other agent-memory settings an operator wrote stay as written. Once
// the identity is born the door refuses it by its key, and nothing is kept.
func TestNotesKeyBeforeBirth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := defaultConfig()
	cfg.SourcePath = path
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	cfg.Genesis.AgentMemory = &AgentMemoryConfig{Extensions: []string{".md"}}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)

	for _, off := range []bool{true, false, true} {
		st, err := a.applyConfigChange(map[string]any{"genesis.agent_memory.off": off})
		if err != nil {
			t.Fatalf("before a birth the door refused off=%v: %v", off, err)
		}
		if st.Genesis.AgentMemoryOff != off {
			t.Errorf("the reply reads back off=%v after saving %v", st.Genesis.AgentMemoryOff, off)
		}
		if a.configState().Genesis.AgentMemoryOff != off {
			t.Errorf("the page's readback says off=%v after saving %v", a.configState().Genesis.AgentMemoryOff, off)
		}
		kept, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if kept.Genesis.AgentMemory == nil || kept.Genesis.AgentMemory.Off != off {
			t.Errorf("the file does not keep off=%v for the birth to read: %+v", off, kept.Genesis.AgentMemory)
		}
		if kept.Genesis.AgentMemory != nil && strings.Join(kept.Genesis.AgentMemory.Extensions, ",") != ".md" {
			t.Errorf("saving the box lost the operator's other agent-memory settings: %+v", kept.Genesis.AgentMemory)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"genesis.agent_memory.off": "yes"}); err == nil || !strings.Contains(err.Error(), "genesis.agent_memory.off: want a boolean") {
		t.Errorf("a value that is not a boolean must be refused by its key: %v", err)
	}

	// FIRST RUN'S OWN DOOR, the handler the page is served by before a
	// birth: it takes this key and reads it back, takes no other key, and
	// serves no other part of the configuration.
	h := a.buildFirstbootHandler()
	if h.SetConfig == nil || h.GetConfig == nil {
		t.Fatal("first run's handler has no door for the notes choice: the page's save is refused as not available")
	}
	st, err := h.SetConfig(map[string]any{"genesis.agent_memory.off": false})
	if err != nil || st.Genesis.AgentMemoryOff || h.GetConfig().Genesis.AgentMemoryOff {
		t.Errorf("first run's door did not save and read back off=false: %+v %v", st, err)
	}
	if _, err := h.SetConfig(map[string]any{"llm.model": "another"}); err == nil || !strings.Contains(err.Error(), "llm.model: before a birth, first run sets genesis.agent_memory.off and nothing else") {
		t.Errorf("first run's door must refuse every other key by name: %v", err)
	}
	if got := h.GetConfig(); got.LLM.Provider != "" || got.LLM.Model != "" {
		t.Errorf("first run's readback serves more than the founding sources and the notes choice: %+v", got.LLM)
	}
	if _, err := h.SetConfig(map[string]any{"genesis.agent_memory.off": true}); err != nil {
		t.Fatal(err)
	}

	// Born: the ledger is there.
	if err := os.WriteFile(cfg.Identity.LedgerPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyConfigChange(map[string]any{"genesis.agent_memory.off": false}); err == nil || !strings.Contains(err.Error(), "genesis.agent_memory.off: the identity is born") {
		t.Errorf("after a birth the door must refuse the key by name: %v", err)
	}
	kept, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Genesis.AgentMemory == nil || !kept.Genesis.AgentMemory.Off {
		t.Errorf("a refused change after birth was kept: %+v", kept.Genesis.AgentMemory)
	}
}
